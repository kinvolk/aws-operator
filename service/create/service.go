package create

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/giantswarm/awstpr"
	awsinfo "github.com/giantswarm/awstpr/aws"
	"github.com/giantswarm/clustertpr/node"
	"github.com/giantswarm/k8scloudconfig"
	microerror "github.com/giantswarm/microkit/error"
	micrologger "github.com/giantswarm/microkit/logger"
	"github.com/juju/errgo"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/api"
	"k8s.io/client-go/pkg/runtime"
	"k8s.io/client-go/pkg/watch"
	"k8s.io/client-go/tools/cache"

	awsutil "github.com/giantswarm/aws-operator/client/aws"
	k8sutil "github.com/giantswarm/aws-operator/client/k8s"
	"github.com/giantswarm/aws-operator/resources"
	awsresources "github.com/giantswarm/aws-operator/resources/aws"
)

const (
	ClusterListAPIEndpoint  string = "/apis/cluster.giantswarm.io/v1/awses"
	ClusterWatchAPIEndpoint string = "/apis/cluster.giantswarm.io/v1/watch/awses"
	// The format of instance's name is "[name of cluster]-[prefix ('master' or 'worker')]-[number]".
	instanceNameFormat string = "%s-%s-%d"
	// The format of prefix inside a cluster "[name of cluster]-[prefix ('master' or 'worker')]".
	instanceClusterPrefixFormat string = "%s-%s"
	// Period or re-synchronizing the list of objects in k8s watcher. 0 means that re-sync will be
	// delayed as long as possible, until the watch will be closed or timed out.
	resyncPeriod time.Duration = 0
	// Prefixes used for machine names.
	prefixMaster string = "master"
	prefixWorker string = "worker"
	// EC2 instance tag keys.
	tagKeyName    string = "Name"
	tagKeyCluster string = "Cluster"
	// Number of retries of RunInstances to wait for Roles to propagate to
	// Instance Profiles
	runInstancesRetries = 10
)

type EC2StateCode int

const (
	// http://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#InstanceState
	EC2PendingState      EC2StateCode = 0
	EC2RunningState      EC2StateCode = 16
	EC2ShuttingDownState EC2StateCode = 32
	EC2TerminatedState   EC2StateCode = 48
	EC2StoppingState     EC2StateCode = 64
	EC2StoppedState      EC2StateCode = 80
)

// Config represents the configuration used to create a version service.
type Config struct {
	// Dependencies.
	AwsConfig  awsutil.Config
	K8sClient  kubernetes.Interface
	Logger     micrologger.Logger
	S3Bucket   string
	PubKeyFile string
}

// DefaultConfig provides a default configuration to create a new version service
// by best effort.
func DefaultConfig() Config {
	return Config{
		// Dependencies.
		K8sClient:  nil,
		Logger:     nil,
		S3Bucket:   "",
		PubKeyFile: "",
	}
}

// New creates a new configured version service.
func New(config Config) (*Service, error) {
	// Dependencies.
	if config.Logger == nil {
		return nil, microerror.MaskAnyf(invalidConfigError, "logger must not be empty")
	}

	newService := &Service{
		// Dependencies.
		awsConfig: config.AwsConfig,
		k8sClient: config.K8sClient,
		logger:    config.Logger,

		// AWS certificates options.
		pubKeyFile: config.PubKeyFile,

		// Internals
		bootOnce: sync.Once{},
	}

	return newService, nil
}

// Service implements the version service interface.
type Service struct {
	// Dependencies.
	awsConfig awsutil.Config
	k8sClient kubernetes.Interface
	logger    micrologger.Logger

	// AWS certificates options.
	pubKeyFile string

	// Internals.
	bootOnce sync.Once
}

type Event struct {
	Type   string
	Object *awstpr.CustomObject
}

func (s *Service) newClusterListWatch() *cache.ListWatch {
	client := s.k8sClient.Core().RESTClient()

	listWatch := &cache.ListWatch{
		ListFunc: func(options api.ListOptions) (runtime.Object, error) {
			req := client.Get().AbsPath(ClusterListAPIEndpoint)
			b, err := req.DoRaw()
			if err != nil {
				return nil, err
			}

			var c awstpr.List
			if err := json.Unmarshal(b, &c); err != nil {
				return nil, err
			}

			return &c, nil
		},

		WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
			req := client.Get().AbsPath(ClusterWatchAPIEndpoint)
			stream, err := req.Stream()
			if err != nil {
				return nil, err
			}

			watcher := watch.NewStreamWatcher(&k8sutil.ClusterDecoder{
				Stream: stream,
			})

			return watcher, nil
		},
	}

	return listWatch
}

func (s *Service) Boot() {
	s.bootOnce.Do(func() {
		if err := s.createTPR(); err != nil {
			panic(err)
		}
		s.logger.Log("info", "successfully created third-party resource")

		_, clusterInformer := cache.NewInformer(
			s.newClusterListWatch(),
			&awstpr.CustomObject{},
			resyncPeriod,
			cache.ResourceEventHandlerFuncs{
				AddFunc: func(obj interface{}) {
					cluster := *obj.(*awstpr.CustomObject)
					s.logger.Log("info", fmt.Sprintf("creating cluster '%s'", cluster.Name))

					if err := s.createClusterNamespace(cluster.Spec.Cluster); err != nil {
						s.logger.Log("error", fmt.Sprintf("could not create cluster namespace: %s", errgo.Details(err)))
						return
					}

					// Create AWS client
					s.awsConfig.Region = cluster.Spec.AWS.Region
					clients := awsutil.NewClients(s.awsConfig)

					err := s.awsConfig.SetAccountID(clients.IAM)
					if err != nil {
						s.logger.Log("error", fmt.Sprintf("could not retrieve amazon account id: %s", errgo.Details(err)))
						return
					}

					// Create keypair
					var keyPair resources.Resource
					var keyPairCreated bool
					{
						var err error
						keyPair = &awsresources.KeyPair{
							ClusterName: cluster.Name,
							Provider:    awsresources.NewFSKeyPairProvider(s.pubKeyFile),
							AWSEntity:   awsresources.AWSEntity{Clients: clients},
						}
						keyPairCreated, err = keyPair.CreateIfNotExists()
						if err != nil {
							s.logger.Log("error", fmt.Sprintf("could not create keypair: %s", errgo.Details(err)))
							return
						}
					}

					if keyPairCreated {
						s.logger.Log("info", fmt.Sprintf("created keypair '%s'", cluster.Name))
					} else {
						s.logger.Log("info", fmt.Sprintf("keypair '%s' already exists, reusing", cluster.Name))
					}

					clusterID := cluster.Spec.Cluster.Cluster.ID
					certs, err := s.getCertsFromSecrets(clusterID)
					if err != nil {
						s.logger.Log("error", fmt.Sprintf("could not get certificates from secrets: %v", errgo.Details(err)))
						return
					}

					// Create KMS key
					var kmsKey resources.ArnResource
					var kmsKeyErr error
					{
						kmsKey = &awsresources.KMSKey{
							AWSEntity: awsresources.AWSEntity{Clients: clients},
						}
						kmsKeyErr = kmsKey.CreateOrFail()
					}

					// Encode TLS assets
					tlsAssets, err := s.encodeTLSAssets(certs, clients.KMS, kmsKey.Arn())
					if err != nil {
						s.logger.Log("error", fmt.Sprintf("could not encode TLS assets: %s", errgo.Details(err)))
						return
					}

					// Create policy
					bucketName := s.bucketName(cluster)

					var policy resources.NamedResource
					var policyErr error
					{
						policy = &awsresources.Policy{
							ClusterID: cluster.Spec.Cluster.Cluster.ID,
							KMSKeyArn: kmsKey.Arn(),
							S3Bucket:  bucketName,
							AWSEntity: awsresources.AWSEntity{Clients: clients},
						}
						policyErr = policy.CreateOrFail()
					}

					// Create S3 bucket
					var bucket resources.Resource
					var bucketCreated bool
					{
						var err error
						bucket = &awsresources.Bucket{
							Name:      bucketName,
							AWSEntity: awsresources.AWSEntity{Clients: clients},
						}
						bucketCreated, err = bucket.CreateIfNotExists()
						if err != nil {
							s.logger.Log("error", fmt.Sprintf("could not create S3 bucket: %s", errgo.Details(err)))
							return
						}
					}

					if bucketCreated {
						s.logger.Log("info", fmt.Sprintf("created bucket '%s'", bucketName))
					} else {
						s.logger.Log("info", fmt.Sprintf("bucket '%s' already exists, reusing", bucketName))
					}

					// Run masters
					anyMastersCreated, masterIDs, err := s.runMachines(runMachinesInput{
						clients:             clients,
						cluster:             cluster,
						tlsAssets:           tlsAssets,
						clusterName:         cluster.Name,
						bucket:              bucket,
						keyPairName:         cluster.Name,
						instanceProfileName: policy.Name(),
						prefix:              prefixMaster,
					})
					if err != nil {
						s.logger.Log("error", errgo.Details(err))
					}

					if !validateIDs(masterIDs) {
						s.logger.Log("error", fmt.Sprintf("master nodes had invalid instance IDs: %v", masterIDs))
						return
					}

					var lb *awsresources.ELB
					lb = &awsresources.ELB{
						Name:   cluster.Spec.Cluster.Cluster.ID,
						AZ:     cluster.Spec.AWS.AZ,
						Client: clients.ELB,
					}
					lbCreated, err := lb.CreateIfNotExists()
					if err != nil {
						s.logger.Log("error", fmt.Sprintf("could not create ELB: %s", errgo.Details(err)))
						return
					}
					if lbCreated {
						s.logger.Log("info", fmt.Sprintf("created ELB '%s'", lb.Name))
					} else {
						s.logger.Log("info", fmt.Sprintf("ELB '%s' already exists, reusing", lb.Name))
					}

					// Run workers
					anyWorkersCreated, _, err := s.runMachines(runMachinesInput{
						clients:             clients,
						cluster:             cluster,
						tlsAssets:           tlsAssets,
						bucket:              bucket,
						clusterName:         cluster.Name,
						keyPairName:         cluster.Name,
						instanceProfileName: policy.Name(),
						prefix:              prefixWorker,
					})
					if err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}

					// If the policy couldn't be created and some instances didn't exist before, that means that the cluster
					// is inconsistent and most problably its deployment broke in the middle during the previous run of
					// aws-operator.
					if (anyMastersCreated || anyWorkersCreated) && (kmsKeyErr != nil || policyErr != nil) {
						s.logger.Log("error", fmt.Sprintf("cluster '%s' is inconsistent, KMS keys and policies were not created, but EC2 instances were missing, please consider deleting this cluster", cluster.Name))
						return
					}

					s.logger.Log("info", fmt.Sprintf("cluster '%s' processed", cluster.Name))
				},
				DeleteFunc: func(obj interface{}) {
					// TODO(nhlfr): Move this to a separate operator.
					cluster := *obj.(*awstpr.CustomObject)

					if err := s.deleteClusterNamespace(cluster.Spec.Cluster); err != nil {
						s.logger.Log("error", "could not delete cluster namespace:", err)
					}

					clients := awsutil.NewClients(s.awsConfig)

					err := s.awsConfig.SetAccountID(clients.IAM)
					if err != nil {
						s.logger.Log("error", fmt.Sprintf("could not retrieve amazon account id: %s", errgo.Details(err)))
						return
					}

					// Delete masters
					if err := s.deleteMachines(deleteMachinesInput{
						clients:     clients,
						clusterName: cluster.Name,
						prefix:      prefixMaster,
					}); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}
					s.logger.Log("info", "deleted masters")

					// Delete workers
					if err := s.deleteMachines(deleteMachinesInput{
						clients:     clients,
						clusterName: cluster.Name,
						prefix:      prefixWorker,
					}); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}
					s.logger.Log("info", "deleted workers")

					// Delete S3 bucket objects
					bucketName := s.bucketName(cluster)

					var bucket resources.Resource
					bucket = &awsresources.Bucket{
						AWSEntity: awsresources.AWSEntity{Clients: clients},
						Name:      bucketName,
					}

					var masterBucketObject resources.Resource
					masterBucketObject = &awsresources.BucketObject{
						Name:      s.bucketObjectName(cluster, prefixMaster),
						Bucket:    bucket.(*awsresources.Bucket),
						AWSEntity: awsresources.AWSEntity{Clients: clients},
					}
					if err := masterBucketObject.Delete(); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}

					var workerBucketObject resources.Resource
					workerBucketObject = &awsresources.BucketObject{
						Name:      s.bucketObjectName(cluster, prefixWorker),
						Bucket:    bucket.(*awsresources.Bucket),
						AWSEntity: awsresources.AWSEntity{Clients: clients},
					}
					if err := workerBucketObject.Delete(); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}

					s.logger.Log("info", "deleted bucket objects")

					// Delete ELB
					var lb resources.Resource
					lb = &awsresources.ELB{
						Name:   cluster.Spec.Cluster.Cluster.ID,
						Client: clients.ELB,
					}
					if err := lb.Delete(); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}

					// Delete policy
					var policy resources.NamedResource
					policy = &awsresources.Policy{
						ClusterID: cluster.Spec.Cluster.Cluster.ID,
						S3Bucket:  bucketName,
						AWSEntity: awsresources.AWSEntity{Clients: clients},
					}
					if err := policy.Delete(); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}
					s.logger.Log("info", "deleted roles, policies, instance profiles")

					// Delete keypair
					var keyPair resources.Resource
					keyPair = &awsresources.KeyPair{
						ClusterName: cluster.Name,
						AWSEntity:   awsresources.AWSEntity{Clients: clients},
					}
					if err := keyPair.Delete(); err != nil {
						s.logger.Log("error", errgo.Details(err))
						return
					}
					s.logger.Log("info", "deleted keypair")

					s.logger.Log("info", fmt.Sprintf("cluster '%s' deleted", cluster.Name))
				},
			},
		)

		s.logger.Log("info", "starting watch")

		// Cluster informer lifecycle can be interrupted by putting a value into a "stop channel".
		// We aren't currently using that functionality, so we are passing a nil here.
		clusterInformer.Run(nil)
	})
}

type instanceNameInput struct {
	clusterName string
	prefix      string
	no          int
}

func instanceName(input instanceNameInput) string {
	return fmt.Sprintf(instanceNameFormat, input.clusterName, input.prefix, input.no)
}

type clusterPrefixInput struct {
	clusterName string
	prefix      string
}

func clusterPrefix(input clusterPrefixInput) string {
	return fmt.Sprintf(instanceClusterPrefixFormat, input.clusterName, input.prefix)
}

type runMachinesInput struct {
	clients             awsutil.Clients
	cluster             awstpr.CustomObject
	tlsAssets           *cloudconfig.CompactTLSAssets
	bucket              resources.Resource
	clusterName         string
	keyPairName         string
	instanceProfileName string
	prefix              string
}

func (s *Service) runMachines(input runMachinesInput) (bool, []string, error) {
	var (
		anyCreated bool

		machines    []node.Node
		awsMachines []awsinfo.Node
		instanceIDs []string
	)

	switch input.prefix {
	case prefixMaster:
		machines = input.cluster.Spec.Cluster.Masters
		awsMachines = input.cluster.Spec.AWS.Masters
	case prefixWorker:
		machines = input.cluster.Spec.Cluster.Workers
		awsMachines = input.cluster.Spec.AWS.Workers
	}

	// TODO(nhlfr): Create a separate module for validating specs and execute on the earlier stages.
	if len(machines) != len(awsMachines) {
		return false, nil, microerror.MaskAny(fmt.Errorf("mismatched number of %s machines in the 'spec' and 'aws' sections: %d != %d",
			input.prefix,
			len(machines),
			len(awsMachines)))
	}

	for i := 0; i < len(machines); i++ {
		name := instanceName(instanceNameInput{
			clusterName: input.clusterName,
			prefix:      input.prefix,
			no:          i,
		})
		created, instanceID, err := s.runMachine(runMachineInput{
			clients:             input.clients,
			cluster:             input.cluster,
			machine:             machines[i],
			awsNode:             awsMachines[i],
			tlsAssets:           input.tlsAssets,
			bucket:              input.bucket,
			clusterName:         input.clusterName,
			keyPairName:         input.keyPairName,
			instanceProfileName: input.instanceProfileName,
			name:                name,
			prefix:              input.prefix,
		})
		if err != nil {
			return false, nil, microerror.MaskAny(err)
		}
		if created {
			anyCreated = true
		}

		instanceIDs = append(instanceIDs, instanceID)
	}
	return anyCreated, instanceIDs, nil
}

// if the instance already exists, return (instanceID, false)
// otherwise (nil, true)
func allExistingInstancesMatch(instances *ec2.DescribeInstancesOutput, state EC2StateCode) (*string, bool) {
	// If the instance doesn't exist, then the Reservations field should be nil.
	// Otherwise, it will contain a slice of instances (which is going to contain our one instance we queried for).
	// TODO(nhlfr): Check whether the instance has correct parameters. That will be most probably done when we
	// will introduce the interface for creating, deleting and updating resources.
	if instances.Reservations != nil {
		for _, r := range instances.Reservations {
			for _, i := range r.Instances {
				if *i.State.Code != int64(state) {
					return i.InstanceId, false
				}
			}
		}
	}
	return nil, true
}

func (s *Service) uploadCloudconfigToS3(svc *s3.S3, s3Bucket, path, data string) error {
	if _, err := svc.PutObject(&s3.PutObjectInput{
		Body:          strings.NewReader(data),
		Bucket:        aws.String(s3Bucket),
		Key:           aws.String(path),
		ContentLength: aws.Int64(int64(len(data))),
	}); err != nil {
		return microerror.MaskAny(err)
	}

	return nil
}

type runMachineInput struct {
	clients             awsutil.Clients
	cluster             awstpr.CustomObject
	machine             node.Node
	awsNode             awsinfo.Node
	tlsAssets           *cloudconfig.CompactTLSAssets
	bucket              resources.Resource
	clusterName         string
	keyPairName         string
	instanceProfileName string
	name                string
	prefix              string
}

func (s *Service) runMachine(input runMachineInput) (bool, string, error) {
	cloudConfigParams := cloudconfig.CloudConfigTemplateParams{
		Cluster:   input.cluster.Spec.Cluster,
		Node:      input.machine,
		TLSAssets: *input.tlsAssets,
	}

	cloudConfig, err := s.cloudConfig(input.prefix, cloudConfigParams, input.cluster.Spec)
	if err != nil {
		return false, "", microerror.MaskAny(err)
	}

	// We now upload the instance cloudconfig to S3 and create a "small
	// cloudconfig" that just fetches the previously uploaded "final
	// cloudconfig" and executes coreos-cloudinit with it as argument.
	// We do this to circumvent the 16KB limit on user-data for EC2 instances.
	cloudconfigConfig := SmallCloudconfigConfig{
		MachineType: input.prefix,
		Region:      input.cluster.Spec.AWS.Region,
		S3DirURI:    s.bucketObjectFullDirPath(input.cluster),
	}

	var cloudconfigS3 resources.Resource
	cloudconfigS3 = &awsresources.BucketObject{
		Name:      s.bucketObjectName(input.cluster, input.prefix),
		Data:      cloudConfig,
		Bucket:    input.bucket.(*awsresources.Bucket),
		AWSEntity: awsresources.AWSEntity{Clients: input.clients},
	}
	if err := cloudconfigS3.CreateOrFail(); err != nil {
		return false, "", microerror.MaskAny(err)
	}

	smallCloudconfig, err := s.SmallCloudconfig(cloudconfigConfig)
	if err != nil {
		return false, "", microerror.MaskAny(err)
	}

	var instance *awsresources.Instance
	var instanceCreated bool
	{
		var err error
		instance = &awsresources.Instance{
			Name:                   input.name,
			ClusterName:            input.clusterName,
			ImageID:                input.awsNode.ImageID,
			InstanceType:           input.awsNode.InstanceType,
			KeyName:                input.keyPairName,
			MinCount:               1,
			MaxCount:               1,
			SmallCloudconfig:       smallCloudconfig,
			IamInstanceProfileName: input.instanceProfileName,
			PlacementAZ:            input.cluster.Spec.AWS.AZ,
			AWSEntity:              awsresources.AWSEntity{Clients: input.clients},
		}
		instanceCreated, err = instance.CreateIfNotExists()
		if err != nil {
			return false, "", microerror.MaskAny(err)
		}
	}

	if instanceCreated {
		s.logger.Log("info", fmt.Sprintf("instance '%s' reserved", input.name))
	} else {
		s.logger.Log("info", fmt.Sprintf("instance '%s' already exists, reusing", input.name))
	}

	s.logger.Log("info", fmt.Sprintf("instance '%s' tagged", input.name))

	return instanceCreated, instance.InstanceID, nil
}

type deleteMachinesInput struct {
	clients     awsutil.Clients
	spec        awstpr.Spec
	clusterName string
	prefix      string
}

func (s *Service) deleteMachines(input deleteMachinesInput) error {
	pattern := clusterPrefix(clusterPrefixInput{
		clusterName: input.clusterName,
		prefix:      input.prefix,
	})
	instances, err := awsresources.FindInstances(awsresources.FindInstancesInput{
		Clients: input.clients,
		Pattern: pattern,
	})
	if err != nil {
		return microerror.MaskAny(err)
	}

	for _, instance := range instances {
		if err := instance.Delete(); err != nil {
			return microerror.MaskAny(err)
		}
	}

	return nil
}

type deleteMachineInput struct {
	name    string
	clients awsutil.Clients
	machine node.Node
}

func (s *Service) deleteMachine(input deleteMachineInput) error {
	var instance resources.Resource
	instance = &awsresources.Instance{
		Name: input.name,
	}
	if err := instance.Delete(); err != nil {
		return microerror.MaskAny(err)
	}

	s.logger.Log("info", fmt.Sprintf("instance '%s' removed", input.name))

	return nil
}

func validateIDs(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if id == "" {
			return false
		}
	}

	return true
}
