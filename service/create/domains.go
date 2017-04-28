package create

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/giantswarm/aws-operator/resources"
	awsresources "github.com/giantswarm/aws-operator/resources/aws"
	"github.com/giantswarm/awstpr"
	microerror "github.com/giantswarm/microkit/error"
)

type hostedZoneInput struct {
	Cluster awstpr.CustomObject
	Domain  string
	Client  *route53.Route53
}

type recordSetInput struct {
	Cluster      awstpr.CustomObject
	Client       *route53.Route53
	Resource     resources.DNSNamedResource
	Domain       string
	HostedZoneID string
}

func (s *Service) createHostedZone(input hostedZoneInput) (*awsresources.HostedZone, error) {
	hzName, err := hostedZoneName(input.Domain)
	if err != nil {
		return nil, microerror.MaskAny(err)
	}

	hz := &awsresources.HostedZone{
		Name:    hzName,
		Comment: hostedZoneComment(input.Cluster),
		Client:  input.Client,
	}

	hzCreated, err := hz.CreateIfNotExists()
	if err != nil {
		return nil, microerror.MaskAnyf(err, "error creating hosted zone '%s'", hz.Name)
	}

	if hzCreated {
		s.logger.Log("debug", fmt.Sprintf("created hosted zone '%s'", hz.Name))
	} else {
		s.logger.Log("debug", fmt.Sprintf("hosted zone '%s' already exists, reusing", hz.Name))
	}

	return hz, nil
}

func (s *Service) deleteRecordSet(input recordSetInput) error {
	hzName, err := hostedZoneName(input.Domain)
	if err != nil {
		return microerror.MaskAny(err)
	}

	hz, err := awsresources.NewHostedZoneFromExisting(hzName, input.Client)
	if err != nil {
		return microerror.MaskAny(err)
	}

	rs := &awsresources.RecordSet{
		Client:       input.Client,
		Resource:     input.Resource,
		Domain:       input.Domain,
		HostedZoneID: hz.ID(),
	}

	if err := rs.Delete(); err != nil {
		return microerror.MaskAny(err)
	}

	return nil
}

func (s *Service) createRecordSet(input recordSetInput) error {
	// Create DNS records for LB.
	apiRecordSet := &awsresources.RecordSet{
		Client:       input.Client,
		Resource:     input.Resource,
		Domain:       input.Domain,
		HostedZoneID: input.HostedZoneID,
	}

	if err := apiRecordSet.CreateOrFail(); err != nil {
		return microerror.MaskAnyf(err, "error registering DNS record for '%s'", apiRecordSet.Domain)
	}

	s.logger.Log("debug", "created or reused DNS record for api")

	return nil
}

func hostedZoneComment(cluster awstpr.CustomObject) string {
	return fmt.Sprintf("Hosted zone for cluster %s", cluster.Spec.Cluster.Cluster.ID)
}

type Domain struct {
	Component string
	Random    string
	g8s       string
	Region    string
	Customer  string
	Fixed     string
}

// hostedZoneName removes the first 2 subdomains from the domain
// e.g. etcd.pbmva.g8s.eu-west-1.adidas.aws.giantswarm.io
func hostedZoneName(domain string) (string, error) {
	tmp := strings.SplitN(domain, ".", 6)
	d := Domain{
		Component: tmp[0],
		Random:    tmp[1],
		g8s:       tmp[2],
		Region:    tmp[3],
		Customer:  tmp[4],
		Fixed:     tmp[5],
	}

	if len(tmp) != 6 {
		return "", microerror.MaskAny(malformedCloudConfigKeyError)
	}

	return strings.Join(d.Fixed, ""), nil
}
