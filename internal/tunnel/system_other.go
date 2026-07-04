//go:build !darwin

package tunnel

import (
	"errors"
	"net/netip"
)

// NewSystem is unimplemented off macOS. The routing/DNS mechanics are
// platform-specific; poof only targets macOS for system-wide mode today.
func NewSystem(cfg Config, exitPublicIP, peerTunnelIP netip.Addr) (SystemTunnel, error) {
	return nil, errors.New("tunnel: --system mode is only supported on macOS")
}
