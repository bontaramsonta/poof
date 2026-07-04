package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/bontaramsonta/poof/internal/socks"
	"github.com/bontaramsonta/poof/internal/wgkey"
)

// TestSOCKSOverTunnel is the whole client stack in one process: a SOCKS5
// proxy in front of a netstack tunnel to a fake Exit that serves HTTP.
// It mirrors production (browser → SOCKS → tunnel → exit) with no cloud.
func TestSOCKSOverTunnel(t *testing.T) {
	exitPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		t.Fatal(err)
	}
	laptopPriv, err := wgkey.GeneratePrivate()
	if err != nil {
		t.Fatal(err)
	}
	startFakeExit(t, exitPriv, laptopPriv.Public())

	tn, err := NewNetstack(Config{
		PrivateKey: laptopPriv,
		LocalIP:    netip.MustParseAddr(laptopIP),
		PeerPublic: exitPriv.Public(),
		Endpoint:   netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), exitPort),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tn.WaitForHandshake(ctx); err != nil {
		t.Fatal(err)
	}

	// SOCKS proxy in front of the tunnel, on a free local port.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	proxyAddr := ln.Addr().String()
	ln.Close()
	go socks.NewServer(tn).ListenAndServe(ctx, proxyAddr)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", proxyAddr); err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A SOCKS5 client hitting the exit's in-tunnel HTTP server by its
	// tunnel IP — traffic must traverse SOCKS → tunnel → exit.
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(fmt.Sprintf("http://%s/", exitIP))
	if err != nil {
		t.Fatalf("GET through SOCKS+tunnel: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "through the tunnel" {
		t.Fatalf("got %q", b)
	}
}
