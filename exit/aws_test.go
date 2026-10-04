package exit

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestTagSpecAddsExtraTags(t *testing.T) {
	spec := tagSpec(ec2types.ResourceTypeInstance, "poof-exit", map[string]string{"poof:client": "android"})
	got := map[string]string{}
	for _, tag := range spec.Tags {
		got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	want := map[string]string{"poof": "1", "Name": "poof-exit", "poof:client": "android"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q", k, got[k], v)
		}
	}
}

func TestHasWireGuardRule(t *testing.T) {
	rule := func(proto string, port int32, cidr string) ec2types.IpPermission {
		return ec2types.IpPermission{
			IpProtocol: aws.String(proto),
			FromPort:   aws.Int32(port),
			ToPort:     aws.Int32(port),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String(cidr)}},
		}
	}
	cases := []struct {
		name  string
		perms []ec2types.IpPermission
		want  bool
	}{
		{"none", nil, false},
		{"wireguard", []ec2types.IpPermission{rule("udp", 51820, "0.0.0.0/0")}, true},
		{"tcp", []ec2types.IpPermission{rule("tcp", 51820, "0.0.0.0/0")}, false},
		{"other port", []ec2types.IpPermission{rule("udp", 51821, "0.0.0.0/0")}, false},
		{"narrow cidr", []ec2types.IpPermission{rule("udp", 51820, "10.0.0.0/8")}, false},
	}
	for _, c := range cases {
		if got := hasWireGuardRule(c.perms); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
