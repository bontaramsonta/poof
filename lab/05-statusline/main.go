// Lab 05: watch the status line tick against an in-process tunnel.
// No cloud, no cost — a fake exit + netstack client, some traffic pushed
// through, and the same Status() data the real CLI renders.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/bontaramsonta/poof/internal/tunnel"
	"github.com/bontaramsonta/poof/internal/wgkey"
)

const (
	exitIP   = "10.66.0.1"
	laptopIP = "10.66.0.2"
	exitPort = 51987
)

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

func main() {
	exitPriv, _ := wgkey.GeneratePrivate()
	laptopPriv, _ := wgkey.GeneratePrivate()

	// Fake exit with an HTTP server that returns a chunk of bytes.
	etun, enet, _ := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr(exitIP)}, nil, 1420)
	edev := device.NewDevice(etun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, ""))
	edev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=%s/32\n",
		exitPriv.Hex(), exitPort, laptopPriv.Public().Hex(), laptopIP))
	edev.Up()
	ln, _ := enet.ListenTCP(&net.TCPAddr{Port: 80})
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 64*1024)) // 64 KiB per request
	}))

	// Client tunnel (the real poof code).
	tn, err := tunnel.NewNetstack(tunnel.Config{
		PrivateKey: laptopPriv,
		LocalIP:    netip.MustParseAddr(laptopIP),
		PeerPublic: exitPriv.Public(),
		Endpoint:   netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), exitPort),
	})
	if err != nil {
		panic(err)
	}
	defer tn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tn.WaitForHandshake(ctx)

	client := &http.Client{Transport: &http.Transport{DialContext: tn.DialContext}}
	start := time.Now()

	fmt.Println("watching the status line (pushing 64 KiB every second):")
	for i := 0; i < 6; i++ {
		if resp, err := client.Get("http://" + exitIP + "/"); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		s, _ := tn.Status()
		age := "—"
		if !s.LastHandshake.IsZero() {
			age = time.Since(s.LastHandshake).Round(time.Second).String() + " ago"
		}
		fmt.Printf("\r\033[K\033[32m●\033[0m demo · up %s · handshake %s · ↓ %s ↑ %s",
			time.Since(start).Round(time.Second), age, humanBytes(s.RxBytes), humanBytes(s.TxBytes))
		time.Sleep(time.Second)
	}
	fmt.Println()
}
