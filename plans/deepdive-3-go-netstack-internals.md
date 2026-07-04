# Deep-dive 3 — Go gVisor netstack: a NIC in your process

> A launchpad for a fresh learning conversation. Goal: understand how a full
> TCP/IP stack can run in userspace memory, how you read/write raw IP packets to
> it, and the surprising things that becomes possible.

## Why this topic exists for you

This is the piece you called "very cool" — and it's the reason poof needs **no
root**. In [`internal/tunnel/netstack.go`](../internal/tunnel/netstack.go):

```go
tun, tnet, err := netstack.CreateNetTUN(
    []netip.Addr{cfg.LocalIP}, cfg.DNS, mtu)   // a NIC that exists only in RAM
...
t.net.DialContext(ctx, network, addr)          // dial THROUGH that in-memory stack
```

`CreateNetTUN` returns two things: a `tun.Device` (which the WireGuard engine
reads/writes **raw IP packets** to) and a `*netstack.Net` (a Go-friendly handle
with `DialContext`, `ListenTCP`, DNS). No kernel interface, no `utun`, no sudo.
Our whole test rig in
[`internal/tunnel/tunnel_test.go`](../internal/tunnel/tunnel_test.go) even runs
**two** of these in one process — a fake Exit with an HTTP server *listening
inside its own netstack* — which is why we can test a real tunnel in 0.2s with
zero cloud. See [ADR-0001](../docs/adr/0001-userspace-socks-proxy-not-system-vpn.md).

## The mental model to build

**1. What a TUN device really is.** A TUN device is a file handle that speaks
*raw IP packets*: read from it = "a packet the OS wants to send"; write to it =
"inject a packet as if it arrived." A kernel TUN needs privilege. gVisor's
netstack provides the *same interface* backed by userspace code.

**2. gVisor's `tcpip` stack.** Originally built to sandbox containers (gVisor
intercepts a container's syscalls and runs the network in userspace for
isolation). It implements IP, TCP, UDP, ICMP, ARP as Go code operating on
in-memory buffers. `netstack.CreateNetTUN` wires that stack to a channel-based
endpoint the WireGuard engine drives.

**3. The data path, both directions.**
- *Outbound*: `DialContext` → netstack builds TCP/IP segments → writes IP
  packets to the tun endpoint → WireGuard reads them, encrypts, sends UDP.
- *Inbound*: WireGuard decrypts → writes an IP packet into the tun endpoint →
  netstack processes it → delivers bytes to your `net.Conn`.
- The key insight: **you can see and manipulate the raw `[]byte` IP packets** at
  the tun boundary. That's the superpower.

**4. Why "no root" falls out of this.** Nothing is a kernel object. The OS only
ever sees one ordinary UDP socket (`conn.NewDefaultBind()`); everything
IP-and-above lives in your process. Contrast with the deferred **utun backend**
(the ADR-0001 seam) which *would* need sudo and OS route/DNS mutation.

## Cool applications to explore (you asked!)

- **Userspace/embedded VPN** — exactly poof; also how Tailscale offers a
  userspace networking mode and subnet routers without kernel changes.
- **Per-app networking without namespaces** — give one library its own IP stack;
  test networked code with no privileges, no ports, fully hermetic.
- **Packet inspection / a mini-Wireshark** — tap the tun boundary and decode
  every IP/TCP header your app emits, in-process.
- **Protocol testing & fuzzing** — inject malformed packets into the stack and
  watch it respond; deterministic, fast, CI-friendly.
- **Honeypots / network simulation** — emulate whole subnets of fake hosts in
  one binary; simulate latency/loss by delaying packets at the tun boundary.
- **The next poof step** — the **SOCKS5 proxy** is this exact idea: accept your
  browser's connection, then `tnet.DialContext` it through the tunnel. That's
  Sunday's build.

## Questions to drive the next conversation

- Show the literal bytes of one IP packet at the tun boundary and parse the
  header fields by hand.
- How does `netstack.Net.DialContext` differ from the stdlib `net.Dialer` — what
  does it *not* touch (hint: the OS)?
- What exactly is the channel endpoint between the WireGuard engine and the
  gVisor stack, and where's the copy/allocation cost?
- How would you tap the boundary to log every packet without breaking the flow?
- What are the throughput ceilings of a userspace stack vs a kernel TUN, and
  where do they come from (copies, scheduling, batching)?
- How does netstack do DNS through the tunnel (`cfg.DNS`), and how does that
  become poof's no-leak resolver?

## Anchors in the poof code

- [`internal/tunnel/netstack.go`](../internal/tunnel/netstack.go) —
  `CreateNetTUN`, `DialContext`, the `Config.DNS` field (unused until SOCKS).
- [`internal/tunnel/tunnel_test.go`](../internal/tunnel/tunnel_test.go) — two
  netstacks in one process; `ListenTCP` *inside* the fake Exit.
- [`cmd/poof/main.go`](../cmd/poof/main.go) — `egressIP` hijacks
  `http.Transport.DialContext` onto the netstack: the app-to-tunnel bridge.
- [`docs/adr/0001`](../docs/adr/0001-userspace-socks-proxy-not-system-vpn.md) —
  the no-root rationale and the utun seam.

## References

- gVisor netstack: https://github.com/google/gvisor (`pkg/tcpip`)
- `golang.zx2c4.com/wireguard/tun/netstack` — the adapter poof uses (read its
  ~300 lines; it's the clearest teacher).
- Tailscale blog, "How Tailscale works" & their userspace-networking posts.
- "Writing a TUN/TAP driver" tutorials — grasp the kernel version first, then
  see how netstack mirrors the interface.

## Suggested skills for the next session

- **prototype** — spin up a throwaway that taps the tun boundary and prints
  packets; perfect for "let me play with it."
- **study-setup** — if you want a proper `~/p/study/userspace-networking/`
  course tying netstack, TUN devices, and Go `net` together.
- **teach-me** — one lesson at a time with a gated coding exercise.
