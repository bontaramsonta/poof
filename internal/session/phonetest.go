package session

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bontaramsonta/poof/internal/provision"
	"github.com/bontaramsonta/poof/internal/wgkey"
)

// PhoneTest launches an Exit for a phone running the official WireGuard
// app, prints the phone's config, then reports the Exit's handshake age
// every 30 s until ctx is cancelled, and terminates the Exit. Debug only
// (poof-android#12): the Exit has no Dead-man's switch, only a hard stop.
func PhoneTest(ctx context.Context, opts Options) error {
	region, err := provision.RegionFor(opts.Country)
	if err != nil {
		return err
	}
	serverPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		return err
	}
	clientPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		return err
	}
	userData, err := provision.RenderUserData(provision.ExitParams{
		ServerPrivate:   serverPriv,
		ClientPublic:    clientPriv.Public(),
		ServerTunnelIP:  serverTunnelIP,
		ClientTunnelIP:  clientTunnelIP,
		ListenPort:      wgPort,
		IdleShutdownMin: idleShutdownMin,
		PhoneTest:       true,
	})
	if err != nil {
		return err
	}
	p, err := provision.NewProvisioner(ctx, region, opts.Profile)
	if err != nil {
		return err
	}

	fmt.Printf("→ PHONE TEST: launching exit in %s (%s), hard stop in %d min...\n",
		opts.Country, region, provision.PhoneTestHardStopMin)
	exit, err := p.Launch(ctx, userData)
	if err != nil {
		return err
	}
	defer func() {
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		fmt.Printf("\n→ terminating exit %s...\n", exit.InstanceID)
		if err := p.Terminate(tctx, exit.InstanceID); err != nil {
			fmt.Printf("!! terminate failed: %v\n", err)
			return
		}
		fmt.Println("→ poof. gone.")
	}()

	conf := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = 1.1.1.1

[Peer]
PublicKey = %s
Endpoint = %s:%d
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`, clientPriv.Base64(), clientTunnelIP, serverPriv.Public().Base64(), exit.PublicIP, wgPort)

	fmt.Printf("→ exit %s is up at %s\n\n", exit.InstanceID, exit.PublicIP)
	fmt.Println("Import into the WireGuard app (+ → Scan from QR code):")
	if err := printQR(conf); err != nil {
		fmt.Printf("(no QR: %v — install with `brew install qrencode`, or type this config in)\n\n%s\n", err, conf)
	}
	fmt.Println("Activate the tunnel once cloud-init finishes (~1 min), then lock the phone.")
	fmt.Println("Press Ctrl-C to stop and destroy the exit.")
	fmt.Println("\n  time      handshake age   longest gap")

	return pollHandshakeAges(ctx, p, exit.InstanceID)
}

func printQR(conf string) error {
	if _, err := exec.LookPath("qrencode"); err != nil {
		return err
	}
	cmd := exec.Command("qrencode", "-t", "ansiutf8")
	cmd.Stdin = strings.NewReader(conf)
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

// pollHandshakeAges reads the Exit's console every 30 s and prints each
// new handshake-age line, tracking the longest gap seen since the first
// handshake.
func pollHandshakeAges(ctx context.Context, p *provision.Provisioner, instanceID string) error {
	var lastTS int64
	longest := 0
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		out, err := p.ConsoleOutput(ctx, instanceID)
		if err != nil && ctx.Err() == nil {
			fmt.Printf("  (console read failed: %v)\n", err)
		}
		sc := bufio.NewScanner(strings.NewReader(out))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			i := indexOf(f, provision.PhoneTestLogPrefix)
			if i < 0 || i+2 >= len(f) {
				continue
			}
			ts, err := strconv.ParseInt(f[i+1], 10, 64)
			if err != nil || ts <= lastTS {
				continue
			}
			lastTS = ts
			age := f[i+2]
			if n, err := strconv.Atoi(age); err == nil && n > longest {
				longest = n
			}
			fmt.Printf("  %s  %-14s  %ds\n", time.Unix(ts, 0).Format("15:04:05"), age, longest)
		}
		select {
		case <-ctx.Done():
			fmt.Printf("\nlongest gap between handshakes: %ds\n", longest)
			return nil
		case <-tick.C:
		}
	}
}

func indexOf(fields []string, s string) int {
	for i, f := range fields {
		if f == s {
			return i
		}
	}
	return -1
}
