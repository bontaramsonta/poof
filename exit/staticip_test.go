package exit

import (
	"strings"
	"testing"
)

func TestPickStaticIP(t *testing.T) {
	mine := StaticIP{Name: "groww", Owner: "sourav", PublicIP: "1.1.1.1"}
	theirs := StaticIP{Name: "groww", Owner: "vaisnav", PublicIP: "2.2.2.2"}
	unowned := StaticIP{Name: "groww", PublicIP: "3.3.3.3"}

	cases := []struct {
		name    string
		found   []StaticIP
		wantIP  string
		wantErr string
	}{
		{"none", nil, "", ""},
		{"only another owner's", []StaticIP{theirs}, "", ""},
		{"mine among others", []StaticIP{theirs, mine}, "1.1.1.1", ""},
		{"unowned blocks", []StaticIP{unowned}, "", "unowned"},
		{"unowned blocks even with mine", []StaticIP{mine, unowned}, "", "unowned"},
		{"two of mine", []StaticIP{mine, mine}, "", "release all but one"},
	}
	for _, c := range cases {
		got, err := pickStaticIP(c.found, "ap-south-1", "sourav", "groww")
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		gotIP := ""
		if got != nil {
			gotIP = got.PublicIP
		}
		if gotIP != c.wantIP {
			t.Errorf("%s: got %q, want %q", c.name, gotIP, c.wantIP)
		}
	}
}

func TestValidOwner(t *testing.T) {
	for _, ok := range []string{"sourav", "vaisnav", "a-1"} {
		if err := ValidOwner(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Sourav", "-x", "a b", "vaisnav@discreate.app"} {
		if err := ValidOwner(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
