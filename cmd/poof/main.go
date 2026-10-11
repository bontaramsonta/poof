// Command poof: an ephemeral personal VPN.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bontaramsonta/poof/exit"
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
	cfgPath, err := configPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "up":
		args, err := parseUpArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, "usage: poof up [--system] [--static-ip <name>] [--only <host,...>] [--owner <name>] <country>")
			os.Exit(2)
		}
		if args.owner, err = resolveOwner(args.owner, cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if args.system && os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr,
				"error: --system reroutes the whole machine and needs root.\n"+
					"       re-run with sudo, preserving your AWS env, e.g.:\n"+
					"         sudo --preserve-env=AWS_PROFILE,HOME,AWS_REGION "+
					os.Args[0]+" up --system "+args.country)
			os.Exit(1)
		}
		if err := cmdUp(args, profile, verbose); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "regions":
		fmt.Println("available countries:")
		fmt.Println("  " + strings.Join(exit.Countries(), ", "))
	case "static-ip":
		args, err := parseStaticIPArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, "usage: poof static-ip [--all] | poof static-ip release|claim <country> <name> [--owner <name>]")
			os.Exit(2)
		}
		if err := cmdStaticIP(args, cfgPath, profile); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

type upArgs struct {
	country  string
	system   bool
	staticIP string
	only     []string
	owner    string
}

// parseUpArgs parses `up`'s arguments, in any order.
func parseUpArgs(args []string) (upArgs, error) {
	var u upArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--system":
			u.system = true
		case "--static-ip", "--only", "--owner":
			if i+1 >= len(args) {
				return u, fmt.Errorf("%s needs a value", a)
			}
			i++
			if a == "--owner" {
				u.owner = args[i]
				continue
			}
			if a == "--static-ip" {
				if err := exit.ValidStaticIPName(args[i]); err != nil {
					return u, err
				}
				u.staticIP = args[i]
				continue
			}
			for _, h := range strings.Split(args[i], ",") {
				if h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), "."); h != "" {
					u.only = append(u.only, h)
				}
			}
		default:
			if strings.HasPrefix(a, "-") {
				return u, fmt.Errorf("unknown flag %s", a)
			}
			if u.country != "" {
				return u, fmt.Errorf("unexpected argument %s", a)
			}
			u.country = a
		}
	}
	if u.country == "" {
		return u, fmt.Errorf("missing country")
	}
	if u.system && len(u.only) > 0 {
		return u, fmt.Errorf("--only filters the SOCKS proxy; it cannot be combined with --system")
	}
	return u, nil
}

type staticIPArgs struct {
	action  string // "" lists; "release" or "claim"
	all     bool
	country string
	name    string
	owner   string
}

// parseStaticIPArgs parses `static-ip`'s arguments; flags go anywhere.
func parseStaticIPArgs(args []string) (staticIPArgs, error) {
	var s staticIPArgs
	var pos []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--all":
			s.all = true
		case "--owner":
			if i+1 >= len(args) {
				return s, fmt.Errorf("--owner needs a value")
			}
			i++
			s.owner = args[i]
		default:
			if strings.HasPrefix(a, "-") {
				return s, fmt.Errorf("unknown flag %s", a)
			}
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 {
		return s, nil
	}
	s.action = pos[0]
	if s.action != "release" && s.action != "claim" {
		return s, fmt.Errorf("unknown static-ip command %s", s.action)
	}
	if len(pos) != 3 {
		return s, fmt.Errorf("%s needs <country> <name>", s.action)
	}
	if s.all {
		return s, fmt.Errorf("--all only applies to the listing")
	}
	s.country, s.name = pos[1], pos[2]
	return s, exit.ValidStaticIPName(s.name)
}

func usage() {
	fmt.Fprintln(os.Stderr, `poof — an ephemeral personal VPN

usage:
  poof up <country>            provision an exit, serve a SOCKS5 proxy
  poof up --system <country>   route the WHOLE machine (needs sudo)
  poof regions                 list available countries
  poof static-ip               list your Static IPs (--all: every Owner's)
  poof static-ip release <country> <name>
                               give a Static IP back to AWS
  poof static-ip claim <country> <name>
                               make an unowned Static IP yours

up flags:
  --static-ip <name>   egress from the named Static IP, kept across sessions
  --only <host,...>    proxy only these hosts (and subdomains) through the
                       exit; everything else goes direct

owner:
  up, release and claim need an Owner: owner = <name> in
  ~/.config/poof/config, or --owner <name> on the command.

env:
  AWS_PROFILE     AWS profile to use
  POOF_VERBOSE    set to enable WireGuard debug logging`)
}

func cmdUp(args upArgs, profile string, verbose bool) error {
	country := args.country
	// Ctrl-C cancels ctx; teardown runs regardless via WithoutCancel.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, teardown, err := session.Provision(ctx, session.Options{
		Country: country, Profile: profile, Verbose: verbose, System: args.system,
		StaticIP:      args.staticIP,
		Owner:         args.owner,
		KeepOnFailure: os.Getenv("POOF_KEEP") != "",
	})
	if err != nil {
		return err
	}
	defer teardown()

	fmt.Printf("\n✓ connected. exit is live in %s.\n", country)
	if args.staticIP != "" {
		fmt.Printf("✓ static IP %s: %s (kept after disconnect)\n", args.staticIP, conn.Exit.PublicIP)
	}

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
		srv := socks.NewServer(conn.Proxy)
		if len(args.only) > 0 {
			srv = socks.NewSplitServer(conn.Proxy, &net.Dialer{}, args.only)
		}
		go func() {
			if err := srv.ListenAndServe(ctx, socksAddr); err != nil {
				fmt.Fprintf(os.Stderr, "!! socks proxy stopped: %v\n", err)
			}
		}()
		fmt.Printf("✓ SOCKS5 proxy listening on %s\n", socksAddr)
		if len(args.only) > 0 {
			fmt.Printf("✓ only %s go through the exit; everything else goes direct\n",
				strings.Join(args.only, ", "))
		}
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

// cmdStaticIP lists Static IPs across every region, or releases or
// claims one.
func cmdStaticIP(args staticIPArgs, cfgPath, profile string) error {
	ctx := context.Background()
	if args.action == "" {
		// Listing works without an Owner; it then shows everyone's.
		owner, err := resolveOwner(args.owner, cfgPath)
		all := args.all || err != nil
		for _, region := range exit.AllRegions() {
			p, err := exit.NewProvisioner(ctx, region, profile)
			if err != nil {
				return err
			}
			found, err := p.ListStaticIPs(ctx, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "!! %v\n", err)
				continue
			}
			for _, s := range found {
				if !all && s.Owner != owner && s.Owner != "" {
					continue
				}
				holder := "idle"
				if s.InstanceID != "" {
					holder = "held by " + s.InstanceID
				}
				o := s.Owner
				if o == "" {
					o = "(unowned)"
				}
				fmt.Printf("%-16s %-14s %-12s %-16s %s\n", s.Region, s.Name, o, s.PublicIP, holder)
			}
		}
		return nil
	}
	owner, err := resolveOwner(args.owner, cfgPath)
	if err != nil {
		return err
	}
	region, err := exit.RegionFor(args.country)
	if err != nil {
		return err
	}
	p, err := exit.NewProvisioner(ctx, region, profile)
	if err != nil {
		return err
	}
	if args.action == "claim" {
		s, err := p.ClaimStaticIP(ctx, owner, args.name)
		if err != nil {
			return err
		}
		fmt.Printf("claimed static IP %s (%s) in %s for %s; the address is unchanged\n", s.Name, s.PublicIP, region, owner)
		return nil
	}
	s, err := p.ReleaseStaticIP(ctx, owner, args.name)
	if err != nil {
		return err
	}
	fmt.Printf("released static IP %s (%s) in %s\n", s.Name, s.PublicIP, region)
	return nil
}
