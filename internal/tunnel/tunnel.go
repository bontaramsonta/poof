// Package tunnel provides the WireGuard tunnel the rest of poof dials
// through. It offers two backends behind a common status/close contract
// (the seam from ADR-0001):
//
//   - Netstack: userspace gVisor stack, no root, drives a SOCKS proxy.
//   - System (macOS): a real utun device plus OS routing/DNS changes for
//     whole-machine capture (needs root). See ADR-0004.
package tunnel

import (
	"context"
	"net"
	"time"
)

// Tunnel is a userspace WireGuard link that connections are dialed
// through (the Netstack/SOCKS backend).
type Tunnel interface {
	// DialContext opens a connection through the tunnel, so it egresses
	// from the Exit. Same contract as net.Dialer.DialContext.
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)

	// Status reports liveness and throughput for the status line.
	Status() (Status, error)

	// Close tears down the local end of the tunnel.
	Close() error
}

// StatusReporter is the minimum both backends expose: enough to drive
// the live status line.
type StatusReporter interface {
	Status() (Status, error)
}

// SystemTunnel is the system-wide (utun) backend returned by NewSystem.
// Beyond reporting status it owns OS-level routing/DNS state, so Close
// must reverse everything it changed.
type SystemTunnel interface {
	StatusReporter
	// WaitForHandshake blocks until the Exit answers or ctx expires.
	WaitForHandshake(ctx context.Context) error
	// Close restores routing and DNS, then tears down the device.
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
