# Deep-dive 1 — Kernel networking: netfilter hooks, iptables/nftables, routing

> A launchpad for a fresh learning conversation. Goal: understand how the Linux
> kernel moves, filters, and rewrites packets — taught through the applications
> that use it (port forwarding, home-router NAT, our VPN exit).

## Why this topic exists for you

While building **poof** (`~/p/poof`), the Exit box turns a blank Amazon Linux
into an internet router. The three lines that make that work are in
[`exit/userdata.go`](../exit/userdata.go):

```
echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-poof.conf   # host → router
nft add table ip poof
nft add chain ip poof postrouting "{ type nat hook postrouting priority 100 ; }"
nft add rule  ip poof postrouting oifname "$PRIMARY_IF" masquerade   # SNAT
```

We *proved* this works: a request left our Mac as tunnel IP `10.66.0.2`, and
`api.ipify.org` reported it came from the Exit's public IP `15.207.235.42`. The
masquerade rule is what erased `10.66.0.2` and substituted the Exit's identity.
We also hit a real bug worth remembering: AL2023 ships **nftables only, not
iptables** — see [ADR-0002](../docs/adr/0002-no-ssh-cloudinit-bootstrap.md).

This doc is about *why* those lines do what they do, from the ground up.

## The mental model to build

**1. The netfilter hook chain.** Every packet the kernel touches passes through
fixed hook points. Learn these cold — everything else hangs off them:

```
                    ┌────────────┐   routing    ┌────────┐
   NIC in ─────────▶│ PREROUTING │──▶ decision ─▶│ INPUT  │─▶ local process
                    └────────────┘      │        └────────┘
                                        │        ┌────────┐
                                        └───────▶│FORWARD │─┐
                                                 └────────┘ │
   local process ─▶┌────────┐   routing              ┌──────▼──────┐
                   │ OUTPUT │──▶ decision ───────────▶│ POSTROUTING │─▶ NIC out
                   └────────┘                         └─────────────┘
```

- **PREROUTING** — first touch, before routing. Home of **DNAT** (destination
  rewrite) → this is *port forwarding*.
- **routing decision** — "is this for me (INPUT) or someone else (FORWARD)?"
  The `ip_forward` sysctl is the gate that even allows the FORWARD path.
- **POSTROUTING** — last touch, after routing. Home of **SNAT/MASQUERADE**
  (source rewrite) → this is what poof does.

**2. Tables → chains → rules.** iptables and nftables are both front-ends to the
same netfilter machinery. A *table* groups rules by purpose (`nat`, `filter`),
a *chain* attaches to a hook, a *rule* is match + action. nftables unifies what
iptables split across fixed tables.

**3. conntrack (connection tracking).** The kernel remembers each NAT
translation, so *replies are un-translated automatically* — you write one
outbound rule, not two. This is why poof needs no explicit reverse rule.

**4. Routing tables vs cryptokey routing.** `ip route` decides which interface a
packet leaves by. WireGuard's `AllowedIPs` is a *second, parallel* routing
table living inside WireGuard. Understand how `wg-quick` bridges them (it adds
kernel routes derived from AllowedIPs).

## Learn it through applications (the fun path)

Work these on a throwaway Linux VM or container; each teaches one concept:

1. **Port forwarding** = DNAT in PREROUTING. Forward `:8080` on a box to an
   internal `:80`. `nft add rule ip nat prerouting tcp dport 8080 dnat to ...`
2. **Home-router NAT** = MASQUERADE in POSTROUTING — you rebuild poof's rule and
   share one public IP among many hosts.
3. **A firewall** = `filter` table, INPUT chain, default-drop policy + allow
   rules. (How the poof security group's job would look *inside* the box.)
4. **Transparent proxy** = TPROXY / REDIRECT — how tools like mitmproxy or a
   sidecar intercept traffic without the app knowing.
5. **Container networking** = a bridge + veth pairs + masquerade — how Docker
   gives containers internet. It's poof's exit pattern in miniature.

## Questions to drive the next conversation

- Walk a single packet through all five hooks with concrete before/after
  addresses, for both a DNAT (port-forward) and a MASQUERADE (poof) case.
- Why does SNAT belong in POSTROUTING and DNAT in PREROUTING — what breaks if
  you swap them?
- How does conntrack match a reply to the original flow? What's a "connection"
  for a stateless protocol like UDP?
- `iptables-legacy` vs `iptables-nft` vs pure `nft` — what actually changed, and
  why did distros migrate?
- How does `wg-quick` translate `AllowedIPs = 0.0.0.0/0` into kernel routes, and
  why does that need `Table` / `fwmark` tricks to avoid a routing loop?

## Anchors in the poof code

- [`exit/userdata.go`](../exit/userdata.go) — the
  nft masquerade + `ip_forward`, and the `wg-quick@wg0` that adds routes.
- [`docs/adr/0002`](../docs/adr/0002-no-ssh-cloudinit-bootstrap.md) — why
  nftables (the iptables-missing bug).
- The security group in [`exit/aws.go`](../exit/aws.go)
  is a *cloud* firewall — contrast it with an in-box `filter` table.

## References

- nftables wiki: https://wiki.nftables.org/ (start: "Simple rule management")
- "A Deep Dive into Iptables and Netfilter Architecture" (DigitalOcean guide)
- `man 8 nft`, `man 7 conntrack`
- Kernel docs: `Documentation/networking/nf_conntrack-sysctl.rst`

## Suggested skills for the next session

- **study-setup** — if you want this as a structured, scenario-driven course
  (`~/p/study/linux-networking/`), matching your existing study-repo pattern.
- **teach-me** — one interactive lesson at a time with a hands-on exercise.
- **diagnose** — if you build the VM lab and a rule doesn't behave, use the
  disciplined repro→instrument loop.
