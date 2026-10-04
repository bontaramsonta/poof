# Deep-dive 2 — WireGuard internals: Noise handshake, session keys, ChaCha20-Poly1305

> A launchpad for a fresh learning conversation. Goal: understand the
> cryptography that turns two keypairs into an encrypted tunnel — the handshake,
> the key schedule, and the per-packet AEAD. Pairs directly with your
> `crypto-foundations` study repo.

## Why this topic exists for you

In **poof** (`~/p/poof`) we generated identities as raw Curve25519 keypairs in
[`wgkey/wgkey.go`](../wgkey/wgkey.go) — 32 random bytes, three
clamp bits, times the base point. We handed them to the engine via IPC in
[`internal/tunnel/netstack.go`](../internal/tunnel/netstack.go), and in the logs
we *watched the whole handshake*:

```
peer(assB…okRY) - Sending handshake initiation
peer(assB…okRY) - Received handshake response
```

Two UDP packets, one round trip, and both sides had shared session keys. Then
`persistent_keepalive=25` kept forcing a re-handshake every ~2 min — which is
also the signal our dead-man's switch reads
([ADR-0003](../docs/adr/0003-deadmans-switch-teardown.md)). This doc unpacks
what actually happened in those two packets.

## The mental model to build

**1. The primitives (map each to your crypto study).**
- **Curve25519 (X25519)** — Elliptic-Curve Diffie-Hellman. The clamping in
  `wgkey.GeneratePrivate()` is *the* spec detail; understand why those 3 bits.
- **ChaCha20-Poly1305** — an AEAD: ChaCha20 stream cipher for confidentiality +
  Poly1305 MAC for integrity, combined. This encrypts every data packet.
- **BLAKE2s** — the hash function used throughout the handshake.
- **HKDF** — how a shared DH secret is expanded into multiple keys.

**2. The Noise Protocol Framework.** WireGuard's handshake is an instance of
**Noise (pattern `IK`)** with an optional preshared key. "IK" = the *Initiator*
already **K**nows the responder's static public key (true for poof: the client
is born knowing the Exit's pubkey). Learn Noise's "chaining key + hash" state
machine — how each message mixes in a DH result and ratchets the keys forward.

**3. The two handshake messages.** Initiation and Response. Trace what each
carries (ephemeral pubkeys, encrypted static key, timestamp for replay
defense) and how, by the end, both sides derive **two** symmetric keys — one for
each direction (initiator-send = responder-receive).

**4. The data-key schedule & rotation.** After the handshake, data packets use
ChaCha20-Poly1305 with a **per-packet 64-bit counter as the nonce** (never
reused → no catastrophic nonce collision). Keys rotate on time/volume
(`REKEY_AFTER_TIME ≈ 120s`) — the recurring handshake you saw.

**5. Replay protection & DoS defense.** A sliding-window anti-replay filter on
the receive counter; a **cookie** mechanism (MAC2) that kicks in under load so
an attacker can't cheaply force expensive DH operations. Understand why a
WireGuard port is *silent* to unauthenticated probes (poof relies on this — the
Exit never answers strangers).

## Learn it through building (the fun path)

- **Re-implement the handshake by hand** in Go against the WireGuard whitepaper,
  using your `crypto-foundations` primitives — produce an initiation packet the
  real `wireguard-go` will accept.
- **Decrypt a captured data packet** given the session keys — prove you can
  reproduce the AEAD open.
- **Instrument poof's client** at `LogLevelVerbose` and correlate each log line
  to a step in the state machine.

## Questions to drive the next conversation

- Why exactly those three clamp bits in Curve25519, and what attack does each
  prevent?
- Walk the Noise `IK` chaining-key ratchet message by message: what DH is mixed
  in, what gets encrypted-and-authenticated at each step?
- How do both peers end up with the *same* two keys without ever sending a key?
- Why a counter-as-nonce instead of random nonces — what would break with
  random, and how big is the reuse risk?
- What is the identity-hiding property (why is the initiator's static key sent
  *encrypted*), and what does the timestamp defend against?
- How does the cookie/MAC2 mechanism resist a flood without keeping per-client
  state?

## Anchors in the poof code

- [`wgkey/wgkey.go`](../wgkey/wgkey.go) — Curve25519 keygen,
  clamping, hex vs base64.
- [`internal/tunnel/netstack.go`](../internal/tunnel/netstack.go) — `IpcSet`
  private_key/public_key/endpoint/allowed_ip, `persistent_keepalive`, and the
  `Status()` parse of `last_handshake_time` (rekey cadence).
- [`docs/adr/0003`](../docs/adr/0003-deadmans-switch-teardown.md) — why the
  handshake cadence is load-bearing for teardown.

## References

- WireGuard whitepaper (Donenfeld, 2017): https://www.wireguard.com/papers/wireguard.pdf
  — the primary source; short and readable.
- Noise Protocol Framework spec: https://noiseprotocol.org/noise.html
- RFC 8439 — ChaCha20 and Poly1305 for IETF Protocols
- RFC 7748 — Elliptic Curves for Security (Curve25519, the clamping)
- Source: `golang.zx2c4.com/wireguard/device` (`handshake.go`, `noise-*.go`)

## Suggested skills for the next session

- **teach-me** — if `crypto-foundations` has (or you add) a WireGuard/Noise
  lesson, this drives it Socratically with a coding exercise gated on green
  tests. This is the strongest fit given your crypto study is already running.
- **tdd** — to build the hand-rolled handshake test-first.
