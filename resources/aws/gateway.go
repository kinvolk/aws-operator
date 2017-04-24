package aws

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	microerror "github.com/giantswarm/microkit/error"
)

type Gateway struct {
	Name  string
	VPCID string
	id    string
	AWSEntity
}

func (g Gateway) findExisting() (*ec2.InternetGateway, error) {
	gateways, err := g.Clients.EC2.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{
		Filters: []*ec2.Filter{
			&ec2.Filter{
				Name: aws.String(fmt.Sprintf("tag:%s", tagKeyName)),
				Values: []*string{
					aws.String(g.Name),
				},
			},
		},
	})
	if err != nil {
		return nil, microerror.MaskAny(err)
	}

	if len(gateways.InternetGateways) < 1 {
		return nil, microerror.MaskAny(gatewayFindError)
	}

	return gateways.InternetGateways[0], nil
}

func (g *Gateway) checkIfExists() (bool, error) {
	gateway, err := g.findExisting()
	if err != nil {
		if strings.Contains(err.Error(), gatewayFindError.Error()) {
			return false, nil
		}
		return false, microerror.MaskAny(err)
	}

	g.id = *gateway.InternetGatewayId

	return true, nil
}

func (g *Gateway) CreateIfNotExists() (bool, error) {
	exists, err := g.checkIfExists()
	if err != nil {
		return false, microerror.MaskAny(err)
	}

	if exists {
		return false, nil
	}

	if err := g.CreateOrFail(); err != nil {
		return false, microerror.MaskAny(err)
	}

	return true, nil
}

func (g *Gateway) CreateOrFail() error {
	gateway, err := g.Clients.EC2.CreateInternetGateway(&ec2.CreateInternetGatewayInput{})
	if err != nil {
		return microerror.MaskAny(err)
	}
	gatewayID := *gateway.InternetGateway.InternetGatewayId

	if _, err := g.Clients.EC2.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(gatewayID),
		VpcId:             aws.String(g.VPCID),
	}); err != nil {
		return microerror.MaskAny(err)
	}

	if _, err := g.Clients.EC2.CreateTags(&ec2.CreateTagsInput{
		Resources: []*string{
			aws.String(gatewayID),
		},
		Tags: []*ec2.Tag{
			{
				Key:   aws.String(tagKeyName),
				Value: aws.String(g.Name),
			},
		},
	}); err != nil {
		return microerror.MaskAny(err)
	}

	g.id = gatewayID

	return nil
}

func (g *Gateway) Delete() error {
	gateway, err := g.findExisting()
	if err != nil {
		return microerror.MaskAny(err)
	}

	if _, err := g.Clients.EC2.DeleteInternetGateway(&ec2.DeleteInternetGatewayInput{
		InternetGatewayId: gateway.InternetGatewayId,
	}); err != nil {
		return microerror.MaskAny(err)
	}

	return nil
}

func (g Gateway) ID() string {
	return g.id
}
