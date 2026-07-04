// Lab 02: two complete WireGuard peers in one process, handshaking over
// loopback UDP, with an HTTP request flowing through the encrypted tunnel.
// No root, no kernel TUN — both "network interfaces" are gVisor netstacks.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// genKey is lab 01, except the IPC interface below wants hex, not base64.
func genKey() (privHex, pubHex string) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		panic(err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(priv[:]), hex.EncodeToString(pub)
}

const (
	exitIP     = "10.66.0.1" // tunnel-internal address of the "exit"
	laptopIP   = "10.66.0.2" // tunnel-internal address of the "laptop"
	listenPort = 51900       // real UDP port the exit listens on (loopback)
)

func main() {
	exitPriv, exitPub := genKey()
	laptopPriv, laptopPub := genKey()

	// ----- the "exit" peer (tomorrow: an EC2 box in Tokyo) -----

	// CreateNetTUN returns the fake TUN for the WG engine, plus tnet — our
	// handle to the in-memory TCP/IP stack sitting behind that interface.
	exitTun, exitNet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(exitIP)},
		nil,  // DNS servers — none needed in this lab
		1420, // MTU: 1500 ethernet minus WireGuard's 80-byte worst-case overhead
	)
	if err != nil {
		log.Fatal(err)
	}
	exitDev := device.NewDevice(exitTun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelError, "exit   "))

	// Configure via the UAPI text protocol — the same one `wg setconf`
	// speaks to the kernel. Keys in hex. The peer's allowed_ip is the
	// cryptokey-routing rule: "this pubkey may only be 10.66.0.2".
	err = exitDev.IpcSet(fmt.Sprintf(
		"private_key=%s\n"+
			"listen_port=%d\n"+
			"public_key=%s\n"+
			"allowed_ip=%s/32\n",
		exitPriv, listenPort, laptopPub, laptopIP))
	if err != nil {
		log.Fatal(err)
	}
	if err := exitDev.Up(); err != nil {
		log.Fatal(err)
	}

	// A plain Go HTTP server, but listening INSIDE the exit's netstack:
	// it is only reachable through the tunnel.
	ln, err := exitNet.ListenTCP(&net.TCPAddr{Port: 80})
	if err != nil {
		log.Fatal(err)
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from the other side of the tunnel (you are %s)\n", r.RemoteAddr)
	}))

	// ----- the "laptop" peer (this is poof's client side, verbatim) -----

	laptopTun, laptopNet, err := netstack.CreateNetTUN(
		[]netip.Addr{netip.MustParseAddr(laptopIP)}, nil, 1420)
	if err != nil {
		log.Fatal(err)
	}
	// Verbose logging on this side so we can watch the handshake happen.
	laptopDev := device.NewDevice(laptopTun, conn.NewDefaultBind(),
		device.NewLogger(device.LogLevelVerbose, "laptop "))

	// endpoint= is the peer's real-world UDP address. allowed_ip=0.0.0.0/0
	// means "route everything to this peer" — same line the real client
	// will use, because the exit is our door to the whole internet.
	err = laptopDev.IpcSet(fmt.Sprintf(
		"private_key=%s\n"+
			"public_key=%s\n"+
			"endpoint=127.0.0.1:%d\n"+
			"allowed_ip=0.0.0.0/0\n",
		laptopPriv, exitPub, listenPort))
	if err != nil {
		log.Fatal(err)
	}
	if err := laptopDev.Up(); err != nil {
		log.Fatal(err)
	}

	// An http.Client whose connections are dialed through the laptop's
	// netstack — so they enter the tunnel instead of the OS network.
	client := &http.Client{
		Transport: &http.Transport{DialContext: laptopNet.DialContext},
	}
	resp, err := client.Get("http://" + exitIP + "/")
	if err != nil {
		log.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()

	fmt.Printf("\n=== HTTP %s through the tunnel ===\n%s", resp.Status, body)
}
