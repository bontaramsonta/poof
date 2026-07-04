package tunnel

import (
	"context"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// System is the whole-machine backend: a real utun device plus OS
// routing and DNS changes so every app's traffic egresses from the Exit.
// It needs root. Close() must reverse every change it made — a partial
// teardown leaves the machine's networking broken (see ADR-0004).
type System struct {
	dev    *device.Device
	ifName string
	steps  []restoreStep // applied in order, undone in reverse (LIFO)
}

type restoreStep struct {
	desc string
	undo func() error
}

var _ SystemTunnel = (*System)(nil)

// NewSystem brings up a utun device and reroutes the machine through it.
// exitPublicIP is the Exit's real address (pinned to the physical path);
// peerTunnelIP is the Exit's in-tunnel address (the point-to-point peer).
func NewSystem(cfg Config, exitPublicIP, peerTunnelIP netip.Addr) (SystemTunnel, error) {
	// Capture the current default route BEFORE we touch anything, so we
	// know which gateway to pin the encrypted traffic to.
	routeOut, err := output("route", "-n", "get", "default")
	if err != nil {
		return nil, fmt.Errorf("tunnel: reading default route: %w", err)
	}
	gateway, iface, err := parseDefaultRoute(routeOut)
	if err != nil {
		return nil, err
	}

	tunDev, err := tun.CreateTUN("utun", mtu)
	if err != nil {
		return nil, fmt.Errorf("tunnel: creating utun (run poof with sudo): %w", err)
	}
	name, err := tunDev.Name()
	if err != nil {
		tunDev.Close()
		return nil, fmt.Errorf("tunnel: reading utun name: %w", err)
	}

	dev := device.NewDevice(tunDev, conn.NewDefaultBind(),
		device.NewLogger(deviceLogLevel(cfg.Verbose), "tunnel "))
	if err := dev.IpcSet(buildIPC(cfg)); err != nil {
		dev.Close()
		return nil, fmt.Errorf("tunnel: configuring device: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("tunnel: bringing device up: %w", err)
	}

	s := &System{dev: dev, ifName: name}
	// From here, any failure must roll back everything already applied.
	fail := func(err error) (SystemTunnel, error) {
		s.Close()
		return nil, err
	}

	local, peer := cfg.LocalIP.String(), peerTunnelIP.String()

	// 1. Address the interface as a point-to-point link local -> peer.
	//    Undone implicitly when the device closes.
	if err := run("ifconfig", name, local, peer, "up"); err != nil {
		return fail(fmt.Errorf("tunnel: addressing %s: %w", name, err))
	}

	// 2. Pin the Exit's public IP to the physical gateway. Without this,
	//    the encrypted WireGuard UDP (which the engine sends to the Exit)
	//    would match the default-override routes below and try to go back
	//    INTO the tunnel — a loop that black-holes the connection.
	exitHost := exitPublicIP.String()
	if err := run("route", "-n", "add", "-host", exitHost, gateway); err != nil {
		return fail(fmt.Errorf("tunnel: pinning exit route: %w", err))
	}
	s.push("exit host route", func() error {
		return run("route", "-n", "delete", "-host", exitHost)
	})

	// 3. Override the default route WITHOUT deleting it, by covering the
	//    whole address space with two more-specific /1 routes via utun.
	//    Restore is simply deleting these two; the original default that
	//    was never removed re-takes effect.
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := run("route", "-n", "add", "-net", cidr, "-interface", name); err != nil {
			return fail(fmt.Errorf("tunnel: adding default-override %s: %w", cidr, err))
		}
		c := cidr
		s.push("default override "+c, func() error {
			return run("route", "-n", "delete", "-net", c, "-interface", name)
		})
	}

	// 4. Point DNS through the tunnel so lookups don't leak to the LAN.
	if err := s.setDNS(iface); err != nil {
		return fail(err)
	}

	return s, nil
}

func (s *System) setDNS(iface string) error {
	listOut, err := output("networksetup", "-listnetworkserviceorder")
	if err != nil {
		return fmt.Errorf("tunnel: listing network services: %w", err)
	}
	service, err := parseServiceForDevice(listOut, iface)
	if err != nil {
		return err
	}
	curOut, err := output("networksetup", "-getdnsservers", service)
	if err != nil {
		return fmt.Errorf("tunnel: reading current DNS: %w", err)
	}
	prev := parseDNSServers(curOut)

	if err := run("networksetup", "-setdnsservers", service, "1.1.1.1"); err != nil {
		return fmt.Errorf("tunnel: setting DNS: %w", err)
	}
	s.push("dns for "+service, func() error {
		if len(prev) == 0 {
			// macOS uses the literal "Empty" to clear DNS overrides.
			return run("networksetup", "-setdnsservers", service, "Empty")
		}
		return run("networksetup", append([]string{"-setdnsservers", service}, prev...)...)
	})
	return nil
}

func (s *System) push(desc string, undo func() error) {
	s.steps = append(s.steps, restoreStep{desc, undo})
}

func (s *System) Status() (Status, error) {
	raw, err := s.dev.IpcGet()
	if err != nil {
		return Status{}, fmt.Errorf("tunnel: reading device state: %w", err)
	}
	return parseStatus(raw), nil
}

func (s *System) WaitForHandshake(ctx context.Context) error {
	return waitForHandshake(ctx, s.Status)
}

// Close restores routing and DNS (in reverse order), then tears down the
// device. It is best-effort: every step runs even if an earlier one
// failed, and all failures are reported together so the user knows if
// manual cleanup is needed.
func (s *System) Close() error {
	var errs []string
	for i := len(s.steps) - 1; i >= 0; i-- {
		if err := s.steps[i].undo(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", s.steps[i].desc, err))
		}
	}
	s.steps = nil
	if s.dev != nil {
		s.dev.Close()
		s.dev = nil
	}
	if len(errs) > 0 {
		return fmt.Errorf("tunnel: teardown incomplete, network may need manual cleanup: %s",
			strings.Join(errs, "; "))
	}
	return nil
}

func run(name string, args ...string) error {
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return nil
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
