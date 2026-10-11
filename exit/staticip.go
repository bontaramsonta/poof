package exit

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// StaticIPTagKey names a Static IP. An Elastic IP tagged poof=1 and
// poof:static-ip=<name> is that name's Static IP in its region.
const StaticIPTagKey = "poof:static-ip"

var staticIPNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// StaticIP is a named Elastic IP that outlives every Exit using it.
type StaticIP struct {
	Region        string
	Name          string
	AllocationID  string
	PublicIP      string
	InstanceID    string // empty while no Exit holds it
	associationID string
}

// ValidStaticIPName reports whether name is usable as a Static IP name.
func ValidStaticIPName(name string) error {
	if !staticIPNameRE.MatchString(name) {
		return fmt.Errorf("exit: static IP name %q must be lowercase letters, digits and dashes", name)
	}
	return nil
}

// LaunchWithStaticIP is Launch, except the Exit egresses from the named
// Static IP, allocated on first use and kept afterwards. It fails while
// another live Exit holds the Static IP; it never steals it.
func (p *Provisioner) LaunchWithStaticIP(ctx context.Context, userData, name string, extraTags map[string]string) (*Exit, error) {
	sip, err := p.EnsureStaticIP(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := p.freeFromDeadHolder(ctx, sip); err != nil {
		return nil, err
	}
	sgID, err := p.EnsureSecurityGroup(ctx)
	if err != nil {
		return nil, err
	}
	tags := map[string]string{StaticIPTagKey: name}
	for k, v := range extraTags {
		tags[k] = v
	}
	id, err := p.RunInstance(ctx, userData, sgID, tags)
	if err != nil {
		return nil, err
	}
	if err := p.AssociateStaticIP(ctx, sip.AllocationID, id); err != nil {
		_ = p.Terminate(context.WithoutCancel(ctx), id)
		return nil, err
	}
	return &Exit{Region: p.region, InstanceID: id, PublicIP: sip.PublicIP}, nil
}

// freeFromDeadHolder detaches a Static IP from an instance that is no
// longer pending or running. EC2 keeps the address on a terminating
// instance for a minute or more, which would otherwise block an
// immediate reconnect. A live holder is an error.
func (p *Provisioner) freeFromDeadHolder(ctx context.Context, sip *StaticIP) error {
	if sip.InstanceID == "" {
		return nil
	}
	holder, err := p.Describe(ctx, sip.InstanceID)
	if err != nil {
		return err
	}
	if holder != nil && (holder.State == string(ec2types.InstanceStateNamePending) ||
		holder.State == string(ec2types.InstanceStateNameRunning)) {
		return fmt.Errorf("exit: static IP %s (%s) is held by %s; disconnect that session first",
			sip.Name, sip.PublicIP, sip.InstanceID)
	}
	_, err = p.ec2.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{AssociationId: aws.String(sip.associationID)})
	if err != nil && apiErrorCode(err) != "InvalidAssociationID.NotFound" {
		return fmt.Errorf("exit: detaching static IP %s from %s: %w", sip.Name, sip.InstanceID, err)
	}
	sip.InstanceID, sip.associationID = "", ""
	return nil
}

// EnsureStaticIP returns the region's Static IP with this name,
// allocating it if missing. Tags are set inside AllocateAddress.
func (p *Provisioner) EnsureStaticIP(ctx context.Context, name string) (*StaticIP, error) {
	if err := ValidStaticIPName(name); err != nil {
		return nil, err
	}
	found, err := p.ListStaticIPs(ctx, name)
	if err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
	case 1:
		return &found[0], nil
	default:
		return nil, fmt.Errorf("exit: %d Elastic IPs in %s are tagged %s=%s; release all but one",
			len(found), p.region, StaticIPTagKey, name)
	}
	out, err := p.ec2.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{
			tagSpec(ec2types.ResourceTypeElasticIp, "poof-static-"+name, map[string]string{StaticIPTagKey: name}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exit: allocating static IP %s in %s: %w", name, p.region, err)
	}
	return &StaticIP{
		Region:       p.region,
		Name:         name,
		AllocationID: aws.ToString(out.AllocationId),
		PublicIP:     aws.ToString(out.PublicIp),
	}, nil
}

// ListStaticIPs lists the region's Static IPs; a non-empty name narrows
// the list to that one.
func (p *Provisioner) ListStaticIPs(ctx context.Context, name string) ([]StaticIP, error) {
	filters := []ec2types.Filter{
		{Name: aws.String("tag:" + TagKey), Values: []string{TagValue}},
		{Name: aws.String("tag-key"), Values: []string{StaticIPTagKey}},
	}
	if name != "" {
		filters = append(filters, ec2types.Filter{Name: aws.String("tag:" + StaticIPTagKey), Values: []string{name}})
	}
	out, err := p.ec2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("exit: listing static IPs in %s: %w", p.region, err)
	}
	found := make([]StaticIP, 0, len(out.Addresses))
	for _, a := range out.Addresses {
		sip := StaticIP{
			Region:        p.region,
			AllocationID:  aws.ToString(a.AllocationId),
			PublicIP:      aws.ToString(a.PublicIp),
			InstanceID:    aws.ToString(a.InstanceId),
			associationID: aws.ToString(a.AssociationId),
		}
		for _, t := range a.Tags {
			if aws.ToString(t.Key) == StaticIPTagKey {
				sip.Name = aws.ToString(t.Value)
			}
		}
		found = append(found, sip)
	}
	return found, nil
}

// AssociateStaticIP waits for the instance to run, then attaches a
// Static IP to it. It never takes the address from another instance.
func (p *Provisioner) AssociateStaticIP(ctx context.Context, allocationID, instanceID string) error {
	// EC2 refuses to associate with a pending instance.
	waiter := ec2.NewInstanceRunningWaiter(p.ec2, func(o *ec2.InstanceRunningWaiterOptions) {
		o.MinDelay = 2 * time.Second
		o.MaxDelay = 5 * time.Second
	})
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}}, 3*time.Minute); err != nil {
		return fmt.Errorf("exit: waiting for %s to run: %w", instanceID, err)
	}
	_, err := p.ec2.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       aws.String(allocationID),
		InstanceId:         aws.String(instanceID),
		AllowReassociation: aws.Bool(false),
	})
	if err != nil {
		return fmt.Errorf("exit: associating static IP with %s: %w", instanceID, err)
	}
	return nil
}

// ReleaseStaticIP gives the named Static IP back to AWS. The address is
// gone for good: anything allowlisting it must be updated.
func (p *Provisioner) ReleaseStaticIP(ctx context.Context, name string) (*StaticIP, error) {
	found, err := p.ListStaticIPs(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("exit: expected 1 static IP named %s in %s, found %d", name, p.region, len(found))
	}
	sip := found[0]
	if err := p.freeFromDeadHolder(ctx, &sip); err != nil {
		return nil, err
	}
	if _, err := p.ec2.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(sip.AllocationID)}); err != nil {
		return nil, fmt.Errorf("exit: releasing static IP %s: %w", name, err)
	}
	return &sip, nil
}
