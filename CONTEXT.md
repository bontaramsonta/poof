# Poof

An ephemeral personal VPN: `poof up japan` provisions a fresh WireGuard exit in a chosen country, connects to it, and destroys it the moment you disconnect. Nothing persists between sessions.

## Language

**Session**:
The lifetime of one `poof up` invocation, from provisioning to teardown. The running foreground process *is* the session; there is exactly one Exit per Session and no record of a Session survives it.
_Avoid_: connection, tunnel instance

**Exit**:
The remote, single-use WireGuard server whose location determines where traffic appears to originate. Born configured, dies with the Session.
_Avoid_: server, node, instance (the cloud machine is an implementation detail of the Exit)

**Country**:
What the user picks (`poof up japan`). Poof maps a Country to a concrete provider location; the user never chooses regions or datacenters directly.
_Avoid_: region, location, zone

**Dead-man's switch**:
The Exit's self-destruct mechanism: if it hasn't heard from the client for a few minutes, it destroys itself. Guarantees the ephemerality promise even when the client dies uncleanly. It is the only backstop against an orphaned Exit; there is no sweep command.
_Avoid_: watchdog, timeout

**Proxy**:
The local doorway into a *proxy-mode* Session — apps that talk to the Proxy have their traffic (including name lookups) emerge from the Exit. Apps that don't are untouched; proxy mode never reconfigures the system.
_Avoid_: listener, endpoint

**Mode**:
How a Session captures traffic. *Proxy mode* (default) serves a SOCKS Proxy and touches nothing else — per-app, no root. *System mode* (`--system`) reroutes the whole machine through the Exit via a real network interface, and must restore the machine's routing and DNS on teardown — whole-machine, needs root.
_Avoid_: tunnel type

## Flagged ambiguities

- **"Disconnect" has two faces.** A *clean* disconnect (Ctrl-C) tears the Exit down immediately; a *dirty* disconnect (crash, lost network) is only detected by the Dead-man's switch, so the Exit lives a few minutes longer. Both end the Session; only the speed differs.
- **"VPN" now has two Modes.** Proxy mode carries only traffic pointed at the Proxy (per-app); system mode carries the whole machine. "VPN" unqualified means whichever Mode the Session is in.

## Example dialogue

**Dev:** If my laptop battery dies mid-Session, is the Session over?
**Expert:** Yes — the Session died with the process. The Exit just doesn't know yet; the Dead-man's switch tells it within a few minutes.
**Dev:** And if the switch ever fails, the Exit leaks forever?
**Expert:** The switch runs on the Exit itself, with shutdown-means-terminate, so it needs nothing from the client. There is no second backstop; if you ever doubt it, look for `poof=1` instances in the console.
**Dev:** When I pick a Country, do I get the same Exit as last time?
**Expert:** There is no "last time." Every Session creates a brand-new Exit with brand-new keys. Nothing is reused, nothing is remembered.
