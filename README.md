# poof

An ephemeral personal VPN. Pick a country; a fresh WireGuard exit appears there, your traffic comes out of it, and the moment you disconnect — poof, it's gone.

```
poof up japan             # proxy mode: SOCKS5 on localhost:1080 (per-app, no root)
sudo poof up --system japan  # system mode: route the WHOLE machine (needs root)
poof regions              # list available countries
```

**Proxy mode** (default): point a browser or `curl --proxy socks5h://localhost:1080` at the proxy. No root, nothing on your system is reconfigured.

**System mode** (`--system`): every app on the machine egresses from the Exit. Needs root — it creates a `utun` device and rewrites routing + DNS, restoring both on exit (see [ADR-0004](./docs/adr/0004-system-wide-utun-routing.md)). Run it preserving your AWS env:

```
sudo --preserve-env=AWS_PROFILE,HOME,AWS_REGION poof up --system japan
```

Either way, Ctrl-C tears the exit down. If the client dies uncleanly, the exit's dead-man's switch self-destructs it within ~5 minutes.

Design vocabulary lives in [CONTEXT.md](./CONTEXT.md); the load-bearing decisions are in [docs/adr/](./docs/adr/).

## Shape

- **Client**: single Go binary. Embeds wireguard-go via `tun/netstack` (no root), speaks SOCKS5 locally, resolves hostnames *through* the tunnel (1.1.1.1) so DNS never leaks.
- **Exit**: `t4g.nano` on Amazon Linux 2023, default VPC, kernel WireGuard, configured entirely by cloud-init user-data. No SSH. Launches into one persistent security group per region, `poof-wireguard`, which opens only UDP 51820 and is never deleted. Tagged `poof=1`.
- **Packages**: `exit` (Country map, user-data, EC2 launch/terminate) and `wgkey` are public, so other clients — the [poof-android](https://github.com/bontaramsonta/poof-android) control plane — launch interchangeable Exits.
- **State**: none. Fresh keypairs per session, generated in memory. The EC2 tag is the only durable record.

## Weekend build order

1. **Sat AM — tunnel core**: netstack device + hardcoded peer config; prove a handshake and an HTTP GET through the tunnel against a hand-made WG server.
2. **Sat PM — provisioner**: EC2 launch with rendered user-data (wg0.conf, sysctl, nftables, dead-man's switch timer), poll until handshake answers, terminate on Ctrl-C.
3. **Sun AM — SOCKS5 + remote DNS**: proxy listener dialing via netstack `DialContext`, domain-type addresses resolved over the tunnel.
4. **Sun PM — CLI polish**: country→region map, `regions`, `nuke` sweep, live status line (handshake age, bytes in/out).
