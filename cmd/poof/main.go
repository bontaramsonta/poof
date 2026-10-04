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
	"github.com/bontaramsonta/poof/internal/socks"
)

const socksAddr = "127.0.0.1:1080"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	profile := os.Getenv("AWS_PROFILE")
	verbose := os.Getenv("POOF_VERBOSE") != ""

	switch os.Args[1] {
	case "up":
		country, system := parseUpArgs(os.Args[2:])
		if country == "" {
			fmt.Fprintln(os.Stderr, "usage: poof up [--system] <country>")
			os.Exit(2)
		}
		if system && os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr,
				"error: --system reroutes the whole machine and needs root.\n"+
					"       re-run with sudo, preserving your AWS env, e.g.:\n"+
					"         sudo --preserve-env=AWS_PROFILE,HOME,AWS_REGION "+
					os.Args[0]+" up --system "+country)
			os.Exit(1)
		}
		if os.Getenv("POOF_PHONE_TEST") != "" {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			err := session.PhoneTest(ctx, session.Options{Country: country, Profile: profile})
			stop()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			return
		}
		if err := cmdUp(country, profile, verbose, system); err != nil {
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

// parseUpArgs extracts the country and the --system flag from `up`'s
// arguments, in any order.
func parseUpArgs(args []string) (country string, system bool) {
	for _, a := range args {
		switch a {
		case "--system":
			system = true
		default:
			if country == "" && !strings.HasPrefix(a, "-") {
				country = a
			}
		}
	}
	return country, system
}

func usage() {
	fmt.Fprintln(os.Stderr, `poof — an ephemeral personal VPN

usage:
  poof up <country>            provision an exit, serve a SOCKS5 proxy
  poof up --system <country>   route the WHOLE machine (needs sudo)
  poof regions                 list available countries
  poof nuke                    destroy every exit poof made, everywhere

env:
  AWS_PROFILE     AWS profile to use
  POOF_VERBOSE    set to enable WireGuard debug logging`)
}

func cmdUp(country, profile string, verbose, system bool) error {
	// Ctrl-C cancels ctx; teardown runs regardless via WithoutCancel.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, teardown, err := session.Provision(ctx, session.Options{
		Country: country, Profile: profile, Verbose: verbose, System: system,
		KeepOnFailure: os.Getenv("POOF_KEEP") != "",
	})
	if err != nil {
		return err
	}
	defer teardown()

	fmt.Printf("\n✓ connected. exit is live in %s.\n", country)

	label := country
	if conn.IsSystem() {
		// Whole machine is already routed; a plain request egresses from
		// the Exit with no proxy configured.
		if ip := egressIP(ctx, http.DefaultClient); ip != "" {
			fmt.Printf("✓ ALL traffic now exits from: %s\n", ip)
		}
		fmt.Println("\n  every app on this machine is going through the exit.")
		label = country + " · system"
	} else {
		// Prove egress through the proxy dialer, then serve SOCKS5.
		proxyClient := &http.Client{
			Transport: &http.Transport{DialContext: conn.Proxy.DialContext},
			Timeout:   10 * time.Second,
		}
		if ip := egressIP(ctx, proxyClient); ip != "" {
			fmt.Printf("✓ traffic through the tunnel exits from: %s\n", ip)
		}
		go func() {
			if err := socks.NewServer(conn.Proxy).ListenAndServe(ctx, socksAddr); err != nil {
				fmt.Fprintf(os.Stderr, "!! socks proxy stopped: %v\n", err)
			}
		}()
		fmt.Printf("✓ SOCKS5 proxy listening on %s\n", socksAddr)
		fmt.Printf("\n  point an app at it, e.g.:\n"+
			"    curl --proxy socks5h://%s https://api.ipify.org\n"+
			"    (socks5h = resolve DNS through the tunnel, no leaks)\n", socksAddr)
	}

	fmt.Println("\n(press Ctrl-C to disconnect and destroy the exit)")

	// Live status line until Ctrl-C.
	runStatusLine(ctx, conn, label)
	fmt.Println()
	return nil
}

// runStatusLine refreshes a single in-place line with liveness and
// throughput until ctx is cancelled. All data comes from Tunnel.Status
// (parsed from the WireGuard device state each tick).
func runStatusLine(ctx context.Context, conn *session.Connected, country string) {
	start := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	reporter := conn.Reporter()
	render := func() {
		s, err := reporter.Status()
		if err != nil {
			return
		}
		// Dot + handshake age: green when the link is fresh (WireGuard
		// re-handshakes ~every 2min), yellow when it's gone quiet.
		dot, age := "\033[33m●\033[0m", "—"
		if !s.LastHandshake.IsZero() {
			d := time.Since(s.LastHandshake).Round(time.Second)
			age = d.String() + " ago"
			if d < 180*time.Second {
				dot = "\033[32m●\033[0m"
			}
		}
		up := time.Since(start).Round(time.Second)
		// \r returns to line start, \033[K clears to end of line.
		fmt.Printf("\r\033[K%s %s · up %s · handshake %s · ↓ %s ↑ %s",
			dot, country, up, age, humanBytes(s.RxBytes), humanBytes(s.TxBytes))
	}

	render()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			render()
		}
	}
}

// humanBytes formats a byte count with a binary unit suffix.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// egressIP fetches the apparent public IP using the given client — proof
// that traffic really exits from the Exit.
func egressIP(ctx context.Context, client *http.Client) string {
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
