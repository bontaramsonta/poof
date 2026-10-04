package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/bontaramsonta/poof/wgkey"
)

// Config describes one end of a WireGuard link, backend-agnostic.
type Config struct {
	PrivateKey wgkey.Key
	LocalIP    netip.Addr // this end's tunnel-internal address

	PeerPublic wgkey.Key
	Endpoint   netip.AddrPort // the peer's real-world UDP address

	// DNS servers the tunnel's resolver should use. Lookups through
	// them travel inside the tunnel, which is the no-leak DNS story.
	DNS []netip.Addr

	Verbose bool
}

// Netstack is a Tunnel implemented entirely in userspace: the network
// interface is an in-process gVisor stack, so no root is needed.
type Netstack struct {
	dev  *device.Device
	net  *netstack.Net
	peer wgkey.Key
}

var _ Tunnel = (*Netstack)(nil)

// NewNetstack brings up the local end of the tunnel. It returns as soon
// as the device is up; use WaitForHandshake to block until the peer
// actually answers.
func NewNetstack(cfg Config) (*Netstack, error) {
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{cfg.LocalIP}, cfg.DNS, mtu)
	if err != nil {
		return nil, fmt.Errorf("tunnel: creating netstack: %w", err)
	}

	dev := device.NewDevice(tun, conn.NewDefaultBind(),
		device.NewLogger(deviceLogLevel(cfg.Verbose), "tunnel "))
	if err := dev.IpcSet(buildIPC(cfg)); err != nil {
		dev.Close()
		return nil, fmt.Errorf("tunnel: configuring device: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("tunnel: bringing device up: %w", err)
	}

	return &Netstack{dev: dev, net: tnet, peer: cfg.PeerPublic}, nil
}

func (t *Netstack) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return t.net.DialContext(ctx, network, addr)
}

func (t *Netstack) Close() error {
	t.dev.Close()
	return nil
}

// Status parses the device's IPC state for our single peer.
func (t *Netstack) Status() (Status, error) {
	raw, err := t.dev.IpcGet()
	if err != nil {
		return Status{}, fmt.Errorf("tunnel: reading device state: %w", err)
	}
	return parseStatus(raw), nil
}

// WaitForHandshake blocks until the peer completes a handshake or ctx
// expires.
func (t *Netstack) WaitForHandshake(ctx context.Context) error {
	return waitForHandshake(ctx, t.Status)
}
