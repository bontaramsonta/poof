// Package session runs one poof Session: provision an Exit, connect the
// tunnel, and guarantee teardown. The Session is the running process;
// nothing here persists once Run returns.
package session

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/bontaramsonta/poof/internal/provision"
	"github.com/bontaramsonta/poof/internal/tunnel"
	"github.com/bontaramsonta/poof/internal/wgkey"
)

const (
	serverTunnelIP  = "10.66.0.1"
	clientTunnelIP  = "10.66.0.2"
	wgPort          = 51820
	idleShutdownMin = 5
)

// Options configure a Session.
type Options struct {
	Country string
	Profile string
	Verbose bool
	// KeepOnFailure leaves the Exit running if the handshake never
	// completes, printing its ID for console-log debugging instead of
	// tearing it down. Debug use only.
	KeepOnFailure bool
	// System routes the whole machine through the Exit (utun + OS routing
	// changes, needs root) instead of serving a SOCKS proxy.
	System bool
}

// Connected is the live state handed to a caller once the Exit answers.
// Exactly one backend is non-nil, depending on Options.System.
type Connected struct {
	Exit   *provision.Exit
	Proxy  *tunnel.Netstack    // proxy (SOCKS) mode
	System tunnel.SystemTunnel // system-wide mode
}

// Reporter returns whichever backend is live, for the status line.
func (c *Connected) Reporter() tunnel.StatusReporter {
	if c.System != nil {
		return c.System
	}
	return c.Proxy
}

// IsSystem reports whether this is a system-wide session.
func (c *Connected) IsSystem() bool { return c.System != nil }

// Provision launches an Exit for the chosen Country and brings up the
// local tunnel to it, blocking until the first handshake or ctx expiry.
// The caller owns teardown via the returned Teardown func.
func Provision(ctx context.Context, opts Options) (*Connected, func(), error) {
	region, err := provision.RegionFor(opts.Country)
	if err != nil {
		return nil, nil, err
	}

	// Two fresh keypairs, in memory, for this Session only.
	serverPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		return nil, nil, err
	}
	clientPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		return nil, nil, err
	}

	userData, err := provision.RenderUserData(provision.ExitParams{
		ServerPrivate:   serverPriv,
		ClientPublic:    clientPriv.Public(),
		ServerTunnelIP:  serverTunnelIP,
		ClientTunnelIP:  clientTunnelIP,
		ListenPort:      wgPort,
		IdleShutdownMin: idleShutdownMin,
	})
	if err != nil {
		return nil, nil, err
	}

	p, err := provision.NewProvisioner(ctx, region, opts.Profile)
	if err != nil {
		return nil, nil, err
	}

	fmt.Printf("→ launching exit in %s (%s)...\n", opts.Country, region)
	exit, err := p.Launch(ctx, userData)
	if err != nil {
		return nil, nil, err
	}
	fmt.Printf("→ exit %s is up at %s; waiting for it to become a WireGuard server...\n",
		exit.InstanceID, exit.PublicIP)

	teardown := func() {
		// context.WithoutCancel so teardown still runs when ctx was
		// cancelled by Ctrl-C — the whole point of teardown.
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		fmt.Printf("→ terminating exit %s...\n", exit.InstanceID)
		if err := p.Terminate(tctx, exit.InstanceID); err != nil {
			fmt.Printf("!! terminate failed: %v\n   run `poof nuke` to be safe\n", err)
			return
		}
		fmt.Println("→ poof. gone.")
	}

	endpoint, err := netip.ParseAddrPort(fmt.Sprintf("%s:%d", exit.PublicIP, wgPort))
	if err != nil {
		teardown()
		return nil, nil, err
	}

	cfg := tunnel.Config{
		PrivateKey: clientPriv,
		LocalIP:    netip.MustParseAddr(clientTunnelIP),
		PeerPublic: serverPriv.Public(),
		Endpoint:   endpoint,
		DNS:        []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		Verbose:    opts.Verbose,
	}

	// Select the backend. Both expose WaitForHandshake + Close; the
	// status line reads through Connected.Reporter.
	conn := &Connected{Exit: exit}
	var waiter interface {
		WaitForHandshake(context.Context) error
	}
	var closer interface{ Close() error }

	if opts.System {
		exitAddr, err := netip.ParseAddr(exit.PublicIP)
		if err != nil {
			teardown()
			return nil, nil, err
		}
		sys, err := tunnel.NewSystem(cfg, exitAddr, netip.MustParseAddr(serverTunnelIP))
		if err != nil {
			teardown()
			return nil, nil, err
		}
		conn.System, waiter, closer = sys, sys, sys
	} else {
		ns, err := tunnel.NewNetstack(cfg)
		if err != nil {
			teardown()
			return nil, nil, err
		}
		conn.Proxy, waiter, closer = ns, ns, ns
	}

	// Cloud-init installs WireGuard before answering, so allow generous
	// time for the first handshake.
	hsCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	fmt.Println("→ waiting for first handshake (cloud-init is installing WireGuard)...")
	if err := waiter.WaitForHandshake(hsCtx); err != nil {
		closer.Close()
		if opts.KeepOnFailure {
			fmt.Printf("!! handshake failed but keeping exit for debug:\n"+
				"   instance %s in %s at %s\n"+
				"   inspect: aws ec2 get-console-output --profile %s --region %s --instance-id %s\n"+
				"   destroy: poof nuke\n",
				exit.InstanceID, exit.Region, exit.PublicIP,
				opts.Profile, exit.Region, exit.InstanceID)
			return nil, nil, err
		}
		teardown()
		return nil, nil, err
	}

	full := func() {
		// Restore local networking first (system mode), then destroy the
		// Exit — the terminate call then goes over normal internet.
		if err := closer.Close(); err != nil {
			fmt.Printf("!! %v\n", err)
		}
		teardown()
	}
	return conn, full, nil
}
