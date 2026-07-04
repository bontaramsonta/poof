// Command poof: an ephemeral personal VPN.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bontaramsonta/poof/internal/provision"
	"github.com/bontaramsonta/poof/internal/session"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	profile := os.Getenv("AWS_PROFILE")
	verbose := os.Getenv("POOF_VERBOSE") != ""

	switch os.Args[1] {
	case "up":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: poof up <country>")
			os.Exit(2)
		}
		if err := cmdUp(os.Args[2], profile, verbose); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "regions":
		fmt.Println("available countries:")
		fmt.Println("  " + strings.Join(provision.Countries(), ", "))
	case "nuke":
		if err := cmdNuke(profile); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `poof — an ephemeral personal VPN

usage:
  poof up <country>   provision an exit, connect, tear down on Ctrl-C
  poof regions        list available countries
  poof nuke           destroy every exit poof ever created, everywhere

env:
  AWS_PROFILE     AWS profile to use
  POOF_VERBOSE    set to enable WireGuard debug logging`)
}

func cmdUp(country, profile string, verbose bool) error {
	// Ctrl-C cancels ctx; teardown runs regardless via WithoutCancel.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, teardown, err := session.Provision(ctx, session.Options{
		Country: country, Profile: profile, Verbose: verbose,
		KeepOnFailure: os.Getenv("POOF_KEEP") != "",
	})
	if err != nil {
		return err
	}
	defer teardown()

	fmt.Printf("\n✓ connected. exit is live in %s.\n", country)

	// Prove egress: fetch our apparent public IP THROUGH the tunnel.
	if ip := egressIP(ctx, conn); ip != "" {
		fmt.Printf("✓ traffic through the tunnel exits from: %s\n", ip)
	}

	fmt.Println("\n(press Ctrl-C to disconnect and destroy the exit)")
	<-ctx.Done()
	fmt.Println()
	return nil
}

// egressIP fetches the apparent public IP as seen from the far end of
// the tunnel — the proof that traffic really exits from the Exit.
func egressIP(ctx context.Context, conn *session.Connected) string {
	client := &http.Client{
		Transport: &http.Transport{DialContext: conn.Tunnel.DialContext},
		Timeout:   10 * time.Second,
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("  (egress check failed: %v)\n", err)
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}

func cmdNuke(profile string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	regions := provision.AllRegions()
	fmt.Printf("sweeping %d regions for poof exits...\n", len(regions))
	total := 0
	for _, region := range regions {
		p, err := provision.NewProvisioner(ctx, region, profile)
		if err != nil {
			fmt.Printf("  %s: %v\n", region, err)
			continue
		}
		res := p.Nuke(ctx)
		if res.Err != nil {
			fmt.Printf("  %s: %v\n", region, res.Err)
			continue
		}
		if len(res.TerminatedInstance) > 0 || len(res.DeletedSGs) > 0 {
			fmt.Printf("  %s: terminated %d instance(s), deleted %d group(s)\n",
				region, len(res.TerminatedInstance), len(res.DeletedSGs))
			total += len(res.TerminatedInstance)
		}
	}
	fmt.Printf("done. %d exit(s) destroyed.\n", total)
	return nil
}
