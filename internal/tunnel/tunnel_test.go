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

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/bontaramsonta/poof/internal/wgkey"
)

const (
	exitIP   = "10.66.0.1"
	laptopIP = "10.66.0.2"
	exitPort = 51999
)

// startFakeExit runs a complete in-process WireGuard peer with an HTTP
// server behind it — a stand-in for the EC2 Exit, reachable only
// through the tunnel. This rig lets every layer above be tested without
// a cloud box.
func startFakeExit(t *testing.T, exitPriv wgkey.Key, laptopPub wgkey.Key) {
	t.Helper()

	tun, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(exitIP)}, nil, mtu)
	if err != nil {
		t.Fatalf("creating exit netstack: %v", err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelError, "exit "))
	err = dev.IpcSet(fmt.Sprintf(
		"private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=%s/32\n",
		exitPriv.Hex(), exitPort, laptopPub.Hex(), laptopIP))
	if err != nil {
		t.Fatalf("configuring exit: %v", err)
	}
	if err := dev.Up(); err != nil {
		t.Fatalf("bringing exit up: %v", err)
	}
	t.Cleanup(dev.Close)

	ln, err := tnet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		t.Fatalf("listening inside exit: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "through the tunnel")
		})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

func TestNetstackTunnel(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tn.WaitForHandshake(ctx); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{
		Transport: &http.Transport{DialContext: tn.DialContext},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("http://" + exitIP + "/")
	if err != nil {
		t.Fatalf("GET through tunnel: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "through the tunnel" {
		t.Fatalf("unexpected body %q", body)
	}

	s, err := tn.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.LastHandshake.IsZero() || time.Since(s.LastHandshake) > time.Minute {
		t.Fatalf("implausible last handshake: %v", s.LastHandshake)
	}
	if s.RxBytes == 0 || s.TxBytes == 0 {
		t.Fatalf("expected nonzero traffic counters, got rx=%d tx=%d", s.RxBytes, s.TxBytes)
	}
}
