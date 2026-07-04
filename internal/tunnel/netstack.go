package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/bontaramsonta/poof/internal/wgkey"
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

// mtu is 1500 (ethernet) minus WireGuard's worst-case 80-byte overhead.
const mtu = 1420

// keepaliveSec keeps NAT mappings warm and, because it forces the ~2min
// re-handshake cadence even when idle, feeds the Exit's dead-man's
// switch its "client still here" signal (ADR-0003).
const keepaliveSec = 25

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

	logLevel := device.LogLevelError
	if cfg.Verbose {
		logLevel = device.LogLevelVerbose
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(logLevel, "tunnel "))

	ipc := fmt.Sprintf(
		"private_key=%s\n"+
			"public_key=%s\n"+
			"endpoint=%s\n"+
			"allowed_ip=0.0.0.0/0\n"+
			"persistent_keepalive_interval=%d\n",
		cfg.PrivateKey.Hex(), cfg.PeerPublic.Hex(), cfg.Endpoint, keepaliveSec)
	if err := dev.IpcSet(ipc); err != nil {
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

// Status parses the device's IPC state (the same key=value text
// `wg show` renders) for our single peer.
func (t *Netstack) Status() (Status, error) {
	raw, err := t.dev.IpcGet()
	if err != nil {
		return Status{}, fmt.Errorf("tunnel: reading device state: %w", err)
	}

	var s Status
	var hsSec, hsNsec int64
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "last_handshake_time_sec":
			hsSec, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_nsec":
			hsNsec, _ = strconv.ParseInt(v, 10, 64)
		case "rx_bytes":
			s.RxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			s.TxBytes, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if hsSec > 0 {
		s.LastHandshake = time.Unix(hsSec, hsNsec)
	}
	return s, nil
}

// WaitForHandshake blocks until the peer completes a handshake (the
// keepalive triggers one immediately on Up) or ctx expires. This is the
// "is the Exit alive yet?" poll the provisioner relies on.
func (t *Netstack) WaitForHandshake(ctx context.Context) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		s, err := t.Status()
		if err != nil {
			return err
		}
		if !s.LastHandshake.IsZero() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tunnel: no handshake from peer: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
