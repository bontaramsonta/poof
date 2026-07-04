package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

const (
	// tagKey marks everything poof creates so nuke/teardown can find it.
	tagKey   = "poof"
	tagValue = "1"

	instanceType = ec2types.InstanceTypeT4gNano
	wgPort       = 51820

	// al2023SSMParam resolves to the current AL2023 arm64 AMI in
	// whatever region the SSM client is pointed at.
	al2023SSMParam = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
)

// Exit is a provisioned Exit: enough to connect to it and to destroy it.
type Exit struct {
	Region     string
	InstanceID string
	PublicIP   string
}

// Provisioner talks to one AWS region.
type Provisioner struct {
	region string
	ec2    *ec2.Client
	ssm    *ssm.Client
}

// NewProvisioner builds a Provisioner for a region using the given
// shared-config profile (empty = default credential chain).
func NewProvisioner(ctx context.Context, region, profile string) (*Provisioner, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("provision: loading AWS config: %w", err)
	}
	return &Provisioner{
		region: region,
		ec2:    ec2.NewFromConfig(cfg),
		ssm:    ssm.NewFromConfig(cfg),
	}, nil
}

// tagSpec tags a resource type with our marker plus a human label.
func tagSpec(rt ec2types.ResourceType, name string) ec2types.TagSpecification {
	return ec2types.TagSpecification{
		ResourceType: rt,
		Tags: []ec2types.Tag{
			{Key: aws.String(tagKey), Value: aws.String(tagValue)},
			{Key: aws.String("Name"), Value: aws.String(name)},
		},
	}
}

// Launch creates a security group and an instance, returning once the
// instance has a public IP. It does NOT wait for the tunnel — that's
// the client's WaitForHandshake against the returned PublicIP.
func (p *Provisioner) Launch(ctx context.Context, userData string) (*Exit, error) {
	amiID, err := p.latestAMI(ctx)
	if err != nil {
		return nil, err
	}
	sgID, err := p.createSecurityGroup(ctx)
	if err != nil {
		return nil, err
	}

	run, err := p.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:                           aws.String(amiID),
		InstanceType:                      instanceType,
		MinCount:                          aws.Int32(1),
		MaxCount:                          aws.Int32(1),
		SecurityGroupIds:                  []string{sgID},
		UserData:                          aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorTerminate,
		TagSpecifications: []ec2types.TagSpecification{
			tagSpec(ec2types.ResourceTypeInstance, "poof-exit"),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("provision: launching instance: %w", err)
	}
	id := *run.Instances[0].InstanceId

	ip, err := p.waitForPublicIP(ctx, id)
	if err != nil {
		// Best-effort cleanup so a failed launch doesn't orphan a box.
		_ = p.Terminate(context.WithoutCancel(ctx), id)
		return nil, err
	}
	return &Exit{Region: p.region, InstanceID: id, PublicIP: ip}, nil
}

func (p *Provisioner) latestAMI(ctx context.Context) (string, error) {
	out, err := p.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(al2023SSMParam),
	})
	if err != nil {
		return "", fmt.Errorf("provision: resolving AMI in %s: %w", p.region, err)
	}
	return *out.Parameter.Value, nil
}

func (p *Provisioner) createSecurityGroup(ctx context.Context) (string, error) {
	// A unique name per launch avoids collisions with a leftover group.
	name := fmt.Sprintf("poof-%d", time.Now().UnixNano())
	sg, err := p.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(name),
		Description: aws.String("poof ephemeral WireGuard exit"),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpec(ec2types.ResourceTypeSecurityGroup, name),
		},
	})
	if err != nil {
		return "", fmt.Errorf("provision: creating security group: %w", err)
	}
	_, err = p.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: sg.GroupId,
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("udp"),
			FromPort:   aws.Int32(wgPort),
			ToPort:     aws.Int32(wgPort),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("provision: opening WireGuard port: %w", err)
	}
	return *sg.GroupId, nil
}

func (p *Provisioner) waitForPublicIP(ctx context.Context, id string) (string, error) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		out, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{id},
		})
		if err == nil && len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 {
			if ip := out.Reservations[0].Instances[0].PublicIpAddress; ip != nil {
				return *ip, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("provision: waiting for public IP: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// Terminate destroys a single instance by ID.
func (p *Provisioner) Terminate(ctx context.Context, instanceID string) error {
	_, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("provision: terminating %s: %w", instanceID, err)
	}
	return nil
}
