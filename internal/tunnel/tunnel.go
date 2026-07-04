// Package tunnel provides the WireGuard tunnel the rest of poof dials
// through. The Tunnel interface is the seam from ADR-0001: today's
// implementation is userspace netstack (no root); a utun backend for
// system-wide capture can slot in later without touching callers.
package tunnel

import (
	"context"
	"net"
	"time"
)

// Tunnel is a connected WireGuard link to the Exit.
type Tunnel interface {
	// DialContext opens a connection through the tunnel, so it egresses
	// from the Exit. Same contract as net.Dialer.DialContext.
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)

	// Status reports liveness and throughput for the status line.
	Status() (Status, error)

	// Close tears down the local end of the tunnel.
	Close() error
}

// Status is a point-in-time view of the tunnel.
type Status struct {
	// LastHandshake is zero until the first handshake completes. While
	// connected, WireGuard re-handshakes roughly every 2 minutes, so an
	// old value means the link is dead.
	LastHandshake time.Time
	RxBytes       uint64
	TxBytes       uint64
}
