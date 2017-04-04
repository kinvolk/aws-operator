package aws

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elb"
	awsclient "github.com/giantswarm/aws-operator/client/aws"
	microerror "github.com/giantswarm/microkit/error"
)

// ELB is an Elastic Load Balancer
type ELB struct {
	Name          string
	AZ            string
	SecurityGroup string
	Tags          []string
	Client        *elb.ELB
}

func (lb *ELB) CreateIfNotExists() (bool, error) {
	if lb.Client == nil {
		return false, microerror.MaskAny(clientNotInitializedError)
	}

	if err := lb.CreateOrFail(); err != nil {
		if err.Error() == awsclient.ELBAlreadyExists {
			return false, nil
		}

		return false, microerror.MaskAny(err)
	}

	return true, nil
}

func (lb *ELB) CreateOrFail() error {
	if lb.Client == nil {
		return microerror.MaskAny(clientNotInitializedError)
	}
	if _, err := lb.Client.CreateLoadBalancer(&elb.CreateLoadBalancerInput{
		LoadBalancerName: aws.String(lb.Name),
		Listeners: []*elb.Listener{
			{
				InstancePort:     aws.Int64(8080),
				LoadBalancerPort: aws.Int64(8080),
				Protocol:         aws.String("HTTP"),
			},
		},
		AvailabilityZones: []*string{
			aws.String(lb.AZ),
		},
		SecurityGroups: []*string{
			// TODO remove sg hardcoding
			aws.String("sg-cb382ca3"),
		},
	}); err != nil {
		return microerror.MaskAny(err)
	}

	return nil
}

func (lb *ELB) Delete() error {
	if lb.Client == nil {
		return microerror.MaskAny(clientNotInitializedError)
	}
	if _, err := lb.Client.DeleteLoadBalancer(&elb.DeleteLoadBalancerInput{
		LoadBalancerName: aws.String(lb.Name),
	}); err != nil {
		return microerror.MaskAny(err)
	}

	return nil
}