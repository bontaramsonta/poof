package exit

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

const (
	// TagKey and TagValue mark every resource poof creates. Every client
	// (CLI and phone control plane) MUST set it.
	TagKey   = "poof"
	TagValue = "1"

	// SecurityGroupName is the one persistent group per region that every
	// Exit launches into. It is created on demand and never deleted.
	SecurityGroupName = "poof-wireguard"

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

// Instance is what EC2 reports about one poof instance.
type Instance struct {
	Region     string
	InstanceID string
	PublicIP   string // empty until assigned
	State      string // EC2 state name: pending, running, terminated, ...
	Tags       map[string]string
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
		return nil, fmt.Errorf("exit: loading AWS config: %w", err)
	}
	return NewProvisionerFromConfig(cfg, region), nil
}

// NewProvisionerFromConfig builds a Provisioner for a region from an
// already-loaded AWS config, so one config can serve many regions.
func NewProvisionerFromConfig(cfg aws.Config, region string) *Provisioner {
	cfg = cfg.Copy()
	cfg.Region = region
	return &Provisioner{
		region: region,
		ec2:    ec2.NewFromConfig(cfg),
		ssm:    ssm.NewFromConfig(cfg),
	}
}

// Region is the AWS region this Provisioner acts in.
func (p *Provisioner) Region() string { return p.region }

// tagSpec tags a resource type with our marker, a human label, and any
// extra tags the caller adds.
func tagSpec(rt ec2types.ResourceType, name string, extra map[string]string) ec2types.TagSpecification {
	tags := []ec2types.Tag{
		{Key: aws.String(TagKey), Value: aws.String(TagValue)},
		{Key: aws.String("Name"), Value: aws.String(name)},
	}
	for k, v := range extra {
		tags = append(tags, ec2types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return ec2types.TagSpecification{ResourceType: rt, Tags: tags}
}

// Launch ensures the region's security group, then creates an instance,
// returning once it has a public IP. extraTags are added next to poof=1
// and Name, all inside RunInstances: a later CreateTags is never used.
// It does NOT wait for the tunnel — that's the client's WaitForHandshake
// against the returned PublicIP.
func (p *Provisioner) Launch(ctx context.Context, userData string, extraTags map[string]string) (*Exit, error) {
	amiID, err := p.latestAMI(ctx)
	if err != nil {
		return nil, err
	}
	sgID, err := p.EnsureSecurityGroup(ctx)
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
			tagSpec(ec2types.ResourceTypeInstance, "poof-exit", extraTags),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exit: launching instance: %w", err)
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
		return "", fmt.Errorf("exit: resolving AMI in %s: %w", p.region, err)
	}
	return *out.Parameter.Value, nil
}

// EnsureSecurityGroup returns the region's persistent poof-wireguard
// group, creating it if missing and re-adding the WireGuard rule if it
// was removed. It never deletes anything.
func (p *Provisioner) EnsureSecurityGroup(ctx context.Context) (string, error) {
	sg, err := p.findSecurityGroup(ctx)
	if err != nil {
		return "", err
	}
	if sg == nil {
		id, err := p.createSecurityGroup(ctx)
		if err != nil {
			return "", err
		}
		return id, p.openWireGuardPort(ctx, id)
	}
	if !hasWireGuardRule(sg.IpPermissions) {
		if err := p.openWireGuardPort(ctx, *sg.GroupId); err != nil {
			return "", err
		}
	}
	return *sg.GroupId, nil
}

func (p *Provisioner) findSecurityGroup(ctx context.Context) (*ec2types.SecurityGroup, error) {
	out, err := p.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("group-name"),
			Values: []string{SecurityGroupName},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("exit: looking up security group in %s: %w", p.region, err)
	}
	if len(out.SecurityGroups) == 0 {
		return nil, nil
	}
	return &out.SecurityGroups[0], nil
}

func (p *Provisioner) createSecurityGroup(ctx context.Context) (string, error) {
	sg, err := p.ec2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(SecurityGroupName),
		Description: aws.String("poof WireGuard exits (UDP 51820)"),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpec(ec2types.ResourceTypeSecurityGroup, SecurityGroupName, nil),
		},
	})
	if apiErrorCode(err) == "InvalidGroup.Duplicate" {
		// Another client created it between our lookup and create.
		existing, ferr := p.findSecurityGroup(ctx)
		if ferr != nil {
			return "", ferr
		}
		if existing != nil {
			return *existing.GroupId, nil
		}
	}
	if err != nil {
		return "", fmt.Errorf("exit: creating security group: %w", err)
	}
	return *sg.GroupId, nil
}

func (p *Provisioner) openWireGuardPort(ctx context.Context, groupID string) error {
	_, err := p.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("udp"),
			FromPort:   aws.Int32(wgPort),
			ToPort:     aws.Int32(wgPort),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	})
	if err != nil && apiErrorCode(err) != "InvalidPermission.Duplicate" {
		return fmt.Errorf("exit: opening WireGuard port: %w", err)
	}
	return nil
}

func hasWireGuardRule(perms []ec2types.IpPermission) bool {
	for _, perm := range perms {
		if aws.ToString(perm.IpProtocol) != "udp" ||
			aws.ToInt32(perm.FromPort) != wgPort || aws.ToInt32(perm.ToPort) != wgPort {
			continue
		}
		for _, r := range perm.IpRanges {
			if aws.ToString(r.CidrIp) == "0.0.0.0/0" {
				return true
			}
		}
	}
	return false
}

func apiErrorCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	return ""
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
			return "", fmt.Errorf("exit: waiting for public IP: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// Describe reports one instance. It returns (nil, nil) when EC2 no
// longer knows the instance.
func (p *Provisioner) Describe(ctx context.Context, instanceID string) (*Instance, error) {
	out, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if apiErrorCode(err) == "InvalidInstanceID.NotFound" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("exit: describing %s: %w", instanceID, err)
	}
	for _, r := range out.Reservations {
		for _, in := range r.Instances {
			inst := p.toInstance(in)
			return &inst, nil
		}
	}
	return nil, nil
}

// FindLive lists the pending or running instances carrying every given
// tag.
func (p *Provisioner) FindLive(ctx context.Context, tags map[string]string) ([]Instance, error) {
	filters := []ec2types.Filter{{
		Name:   aws.String("instance-state-name"),
		Values: []string{string(ec2types.InstanceStateNamePending), string(ec2types.InstanceStateNameRunning)},
	}}
	for k, v := range tags {
		filters = append(filters, ec2types.Filter{Name: aws.String("tag:" + k), Values: []string{v}})
	}
	var found []Instance
	pages := ec2.NewDescribeInstancesPaginator(p.ec2, &ec2.DescribeInstancesInput{Filters: filters})
	for pages.HasMorePages() {
		out, err := pages.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("exit: listing instances in %s: %w", p.region, err)
		}
		for _, r := range out.Reservations {
			for _, in := range r.Instances {
				found = append(found, p.toInstance(in))
			}
		}
	}
	return found, nil
}

func (p *Provisioner) toInstance(in ec2types.Instance) Instance {
	inst := Instance{
		Region:     p.region,
		InstanceID: aws.ToString(in.InstanceId),
		PublicIP:   aws.ToString(in.PublicIpAddress),
		Tags:       map[string]string{},
	}
	if in.State != nil {
		inst.State = string(in.State.Name)
	}
	for _, t := range in.Tags {
		inst.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return inst
}

// Terminate destroys a single instance by ID.
func (p *Provisioner) Terminate(ctx context.Context, instanceID string) error {
	_, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("exit: terminating %s: %w", instanceID, err)
	}
	return nil
}
