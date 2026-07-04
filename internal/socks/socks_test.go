package socks

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// osDialer sends connections straight to the OS — a stand-in for the
// tunnel that lets us test the SOCKS protocol without WireGuard.
type osDialer struct{}

func (osDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// startProxy runs a SOCKS server on a random port and returns its addr.
func startProxy(t *testing.T, d Dialer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // ListenAndServe re-listens; we just wanted a free port

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go NewServer(d).ListenAndServe(ctx, addr)

	// Wait for it to come up.
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return addr
}

func getThroughProxy(t *testing.T, proxyAddr, targetURL string) string {
	t.Helper()
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
	resp, err := client.Get(targetURL)
	if err != nil {
		t.Fatalf("GET %s through proxy: %v", targetURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestSOCKS5ConnectByIP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok-ip") }))
	defer backend.Close()

	proxyAddr := startProxy(t, osDialer{})
	// httptest URL is http://127.0.0.1:PORT — exercises the IPv4 path.
	if got := getThroughProxy(t, proxyAddr, backend.URL); got != "ok-ip" {
		t.Fatalf("got %q, want ok-ip", got)
	}
}

func TestSOCKS5ConnectByHostname(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok-host") }))
	defer backend.Close()

	// Rewrite the URL host to "localhost" so the SOCKS client sends a
	// DOMAIN address — the ATYP=domain path our DNS story depends on.
	u, _ := url.Parse(backend.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	hostURL := "http://localhost:" + port

	// The osDialer will resolve "localhost" itself, proving the hostname
	// was passed through to the dialer rather than resolved in the proxy.
	proxyAddr := startProxy(t, osDialer{})
	if got := getThroughProxy(t, proxyAddr, hostURL); got != "ok-host" {
		t.Fatalf("got %q, want ok-host", got)
	}
}

// captureDialer records the addr it was asked to dial, then dials the OS.
type captureDialer struct{ got chan string }

func (c captureDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c.got <- addr
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// TestHostnamePassedThrough is the DNS-leak guard: when the client sends
// a hostname, the proxy must hand that hostname (not a resolved IP) to
// the dialer, so resolution happens through the tunnel.
func TestHostnamePassedThrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "x") }))
	defer backend.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))

	cap := captureDialer{got: make(chan string, 1)}
	proxyAddr := startProxy(t, cap)
	// Fire a request to trigger a dial; result is asserted via the
	// captured addr below, so ignore errors here (no t.Fatal in a
	// non-test goroutine).
	go func() {
		dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err != nil {
			return
		}
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			},
			Timeout: 3 * time.Second,
		}
		if resp, err := client.Get("http://localhost:" + port); err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case addr := <-cap.got:
		if host, _, _ := net.SplitHostPort(addr); host != "localhost" {
			t.Fatalf("dialer got %q; hostname was resolved locally (DNS leak!)", addr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dialer never called")
	}
}
