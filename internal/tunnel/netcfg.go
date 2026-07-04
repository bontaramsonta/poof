package tunnel

import (
	"fmt"
	"strings"
)

// These parse the text output of macOS networking tools. They're pure so
// they can be tested without root or a real network. Used only by the
// darwin System backend.

// parseDefaultRoute extracts the gateway IP and interface from the output
// of `route -n get default`.
func parseDefaultRoute(out string) (gateway, iface string, err error) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "gateway:":
			gateway = f[1]
		case "interface:":
			iface = f[1]
		}
	}
	if gateway == "" || iface == "" {
		return "", "", fmt.Errorf("tunnel: no default route found in route output")
	}
	return gateway, iface, nil
}

// parseServiceForDevice maps a BSD device (e.g. "en0") to its macOS
// network-service name (e.g. "Wi-Fi") from the output of
// `networksetup -listnetworkserviceorder`.
func parseServiceForDevice(out, device string) (string, error) {
	var lastService string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// "(1) Wi-Fi" — remember the service name.
		if strings.HasPrefix(line, "(") {
			if i := strings.Index(line, ") "); i > 0 && !strings.HasPrefix(line, "(Hardware") {
				lastService = strings.TrimSpace(line[i+2:])
			}
		}
		// "(Hardware Port: Wi-Fi, Device: en0)" — match the device.
		if strings.Contains(line, "Device: "+device+")") {
			if lastService != "" {
				return lastService, nil
			}
		}
	}
	return "", fmt.Errorf("tunnel: no network service found for device %q", device)
}

// parseDNSServers reads `networksetup -getdnsservers <service>`. macOS
// prints "There aren't any DNS Servers set..." when none are configured,
// which we represent as an empty slice (restore target = "Empty").
func parseDNSServers(out string) []string {
	out = strings.TrimSpace(out)
	if out == "" || strings.HasPrefix(out, "There aren't") {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			servers = append(servers, s)
		}
	}
	return servers
}
