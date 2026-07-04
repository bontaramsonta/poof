package tunnel

import (
	"reflect"
	"testing"
)

func TestParseDefaultRoute(t *testing.T) {
	out := `   route to: default
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`
	gw, iface, err := parseDefaultRoute(out)
	if err != nil {
		t.Fatal(err)
	}
	if gw != "192.168.1.1" || iface != "en0" {
		t.Fatalf("got gw=%q iface=%q", gw, iface)
	}
}

func TestParseDefaultRouteMissing(t *testing.T) {
	if _, _, err := parseDefaultRoute("nothing useful here\n"); err == nil {
		t.Fatal("expected error when no default route present")
	}
}

func TestParseServiceForDevice(t *testing.T) {
	out := `An asterisk (*) denotes that a network service is disabled.
(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(2) Thunderbolt Bridge
(Hardware Port: Thunderbolt Bridge, Device: bridge0)

(3) iPhone USB
(Hardware Port: iPhone USB, Device: en5)
`
	cases := map[string]string{
		"en0":     "Wi-Fi",
		"bridge0": "Thunderbolt Bridge",
		"en5":     "iPhone USB",
	}
	for dev, want := range cases {
		got, err := parseServiceForDevice(out, dev)
		if err != nil {
			t.Fatalf("%s: %v", dev, err)
		}
		if got != want {
			t.Fatalf("device %s: got %q, want %q", dev, got, want)
		}
	}
	if _, err := parseServiceForDevice(out, "en9"); err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestParseDNSServers(t *testing.T) {
	if got := parseDNSServers("There aren't any DNS Servers set on Wi-Fi.\n"); got != nil {
		t.Fatalf("expected nil for unset, got %v", got)
	}
	got := parseDNSServers("8.8.8.8\n8.8.4.4\n")
	want := []string{"8.8.8.8", "8.8.4.4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
