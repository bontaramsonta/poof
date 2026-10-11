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

const (
	// StaticIPTagKey names a Static IP. An Elastic IP tagged poof=1 and
	// poof:static-ip=<name> is that name's Static IP in its region.
	StaticIPTagKey = "poof:static-ip"

	// OwnerTagKey names the Owner of an Exit or Static IP. It keeps
	// people sharing an account apart by convention, not by IAM.
	OwnerTagKey = "poof:owner"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// StaticIP is a named Elastic IP that outlives every Exit using it.
type StaticIP struct {
	Region        string
	Name          string
	Owner         string // empty for a Static IP made before Owners
	AllocationID  string
	PublicIP      string
	InstanceID    string // empty while no Exit holds it
	associationID string
}

// ValidStaticIPName reports whether name is usable as a Static IP name.
func ValidStaticIPName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("exit: static IP name %q must be lowercase letters, digits and dashes", name)
	}
	return nil
}

// ValidOwner reports whether owner is usable as an Owner name.
func ValidOwner(owner string) error {
	if !nameRE.MatchString(owner) {
		return fmt.Errorf("exit: owner %q must be lowercase letters, digits and dashes", owner)
	}
	return nil
}

func staticIPLabel(owner, name string) string { return "poof-static-" + owner + "-" + name }

// LaunchWithStaticIP is Launch, except the Exit egresses from the
// owner's named Static IP, allocated on first use and kept afterwards.
// It fails while another live Exit holds the Static IP; it never steals
// it. The Exit is tagged with the owner too.
func (p *Provisioner) LaunchWithStaticIP(ctx context.Context, userData, owner, name string, extraTags map[string]string) (*Exit, error) {
	sip, err := p.EnsureStaticIP(ctx, owner, name)
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
	tags := map[string]string{StaticIPTagKey: name, OwnerTagKey: owner}
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

// pickStaticIP finds the owner's Static IP among same-named ones. An
// unowned one with that name is an error until claimed: silently
// allocating a second address would break whatever allowlists the first.
func pickStaticIP(found []StaticIP, region, owner, name string) (*StaticIP, error) {
	var mine []StaticIP
	unowned := 0
	for _, s := range found {
		switch s.Owner {
		case owner:
			mine = append(mine, s)
		case "":
			unowned++
		}
	}
	if len(mine) > 1 {
		return nil, fmt.Errorf("exit: %d Elastic IPs in %s are tagged %s=%s for %s; release all but one",
			len(mine), region, StaticIPTagKey, name, owner)
	}
	if unowned > 0 {
		return nil, fmt.Errorf("exit: static IP %s in %s is unowned; claim it with `poof static-ip claim <country> %s`",
			name, region, name)
	}
	if len(mine) == 1 {
		return &mine[0], nil
	}
	return nil, nil
}

// EnsureStaticIP returns the owner's Static IP with this name in the
// region, allocating it if missing. Tags are set inside AllocateAddress.
func (p *Provisioner) EnsureStaticIP(ctx context.Context, owner, name string) (*StaticIP, error) {
	if err := ValidOwner(owner); err != nil {
		return nil, err
	}
	if err := ValidStaticIPName(name); err != nil {
		return nil, err
	}
	found, err := p.ListStaticIPs(ctx, name)
	if err != nil {
		return nil, err
	}
	sip, err := pickStaticIP(found, p.region, owner, name)
	if err != nil || sip != nil {
		return sip, err
	}
	out, err := p.ec2.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
		TagSpecifications: []ec2types.TagSpecification{
			tagSpec(ec2types.ResourceTypeElasticIp, staticIPLabel(owner, name),
				map[string]string{StaticIPTagKey: name, OwnerTagKey: owner}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exit: allocating static IP %s in %s: %w", name, p.region, err)
	}
	return &StaticIP{
		Region:       p.region,
		Name:         name,
		Owner:        owner,
		AllocationID: aws.ToString(out.AllocationId),
		PublicIP:     aws.ToString(out.PublicIp),
	}, nil
}

// ListStaticIPs lists the region's Static IPs of every Owner; a
// non-empty name narrows the list to that name.
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
			switch aws.ToString(t.Key) {
			case StaticIPTagKey:
				sip.Name = aws.ToString(t.Value)
			case OwnerTagKey:
				sip.Owner = aws.ToString(t.Value)
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

// ReleaseStaticIP gives the owner's named Static IP back to AWS. The
// address is gone for good: anything allowlisting it must be updated.
func (p *Provisioner) ReleaseStaticIP(ctx context.Context, owner, name string) (*StaticIP, error) {
	found, err := p.ListStaticIPs(ctx, name)
	if err != nil {
		return nil, err
	}
	sip, err := pickStaticIP(found, p.region, owner, name)
	if err != nil {
		return nil, err
	}
	if sip == nil {
		return nil, fmt.Errorf("exit: %s has no static IP named %s in %s", owner, name, p.region)
	}
	if err := p.freeFromDeadHolder(ctx, sip); err != nil {
		return nil, err
	}
	if _, err := p.ec2.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(sip.AllocationID)}); err != nil {
		return nil, fmt.Errorf("exit: releasing static IP %s: %w", name, err)
	}
	return sip, nil
}

// ClaimStaticIP makes an unowned Static IP the owner's. The address
// does not change. This is the one retag poof does.
func (p *Provisioner) ClaimStaticIP(ctx context.Context, owner, name string) (*StaticIP, error) {
	if err := ValidOwner(owner); err != nil {
		return nil, err
	}
	found, err := p.ListStaticIPs(ctx, name)
	if err != nil {
		return nil, err
	}
	var unowned []StaticIP
	for _, s := range found {
		switch s.Owner {
		case owner:
			return nil, fmt.Errorf("exit: %s already has a static IP named %s in %s (%s)", owner, name, p.region, s.PublicIP)
		case "":
			unowned = append(unowned, s)
		}
	}
	if len(unowned) != 1 {
		return nil, fmt.Errorf("exit: expected 1 unowned static IP named %s in %s, found %d", name, p.region, len(unowned))
	}
	sip := unowned[0]
	_, err = p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{sip.AllocationID},
		Tags: []ec2types.Tag{
			{Key: aws.String(OwnerTagKey), Value: aws.String(owner)},
			{Key: aws.String("Name"), Value: aws.String(staticIPLabel(owner, name))},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("exit: claiming static IP %s: %w", name, err)
	}
	sip.Owner = owner
	return &sip, nil
}
