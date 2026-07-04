package provision

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// NukeResult reports what a sweep of one region destroyed.
type NukeResult struct {
	Region             string
	TerminatedInstance []string
	DeletedSGs         []string
	Err                error
}

// tagFilter matches every resource poof created.
func tagFilter() ec2types.Filter {
	return ec2types.Filter{
		Name:   aws.String("tag:" + tagKey),
		Values: []string{tagValue},
	}
}

// Nuke destroys every poof-tagged instance in this Provisioner's region,
// then deletes leftover poof security groups. Security-group deletion
// fails while an instance still references it, so instances go first and
// SG cleanup is best-effort — a stubborn SG is swept on the next nuke.
func (p *Provisioner) Nuke(ctx context.Context) NukeResult {
	res := NukeResult{Region: p.region}

	inst, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{tagFilter()},
	})
	if err != nil {
		res.Err = fmt.Errorf("provision: describing instances in %s: %w", p.region, err)
		return res
	}
	var ids []string
	for _, r := range inst.Reservations {
		for _, in := range r.Instances {
			// Skip already-terminated instances.
			if in.State != nil && in.State.Name == ec2types.InstanceStateNameTerminated {
				continue
			}
			ids = append(ids, *in.InstanceId)
		}
	}
	if len(ids) > 0 {
		if _, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: ids,
		}); err != nil {
			res.Err = fmt.Errorf("provision: terminating in %s: %w", p.region, err)
			return res
		}
		res.TerminatedInstance = ids
	}

	// Best-effort SG cleanup. Won't succeed until the instances using
	// them are gone, so we don't treat failures as fatal.
	sgs, err := p.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{tagFilter()},
	})
	if err == nil {
		for _, sg := range sgs.SecurityGroups {
			if _, err := p.ec2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
				GroupId: sg.GroupId,
			}); err == nil {
				res.DeletedSGs = append(res.DeletedSGs, *sg.GroupId)
			}
		}
	}
	return res
}
