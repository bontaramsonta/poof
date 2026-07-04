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
The Exit's self-destruct mechanism: if it hasn't heard from the client for a few minutes, it destroys itself. Guarantees the ephemerality promise even when the client dies uncleanly.
_Avoid_: watchdog, timeout

**Nuke**:
The escape hatch (`poof nuke`): find every Exit that poof ever created, in any Country, and destroy it. The only operation that looks beyond the current Session.
_Avoid_: cleanup, gc

**Proxy**:
The local doorway into the Session — apps that talk to the Proxy have their traffic (including name lookups) emerge from the Exit. Apps that don't are untouched; poof never reconfigures the system.
_Avoid_: listener, endpoint

## Flagged ambiguities

- **"Disconnect" has two faces.** A *clean* disconnect (Ctrl-C) tears the Exit down immediately; a *dirty* disconnect (crash, lost network) is only detected by the Dead-man's switch, so the Exit lives a few minutes longer. Both end the Session; only the speed differs.
- **"VPN" is aspirational.** A Session today carries only traffic that is pointed at the Proxy, not the whole machine. System-wide capture is a possible future mode, not a synonym.

## Example dialogue

**Dev:** If my laptop battery dies mid-Session, is the Session over?
**Expert:** Yes — the Session died with the process. The Exit just doesn't know yet; the Dead-man's switch tells it within a few minutes.
**Dev:** And if the switch ever fails, the Exit leaks forever?
**Expert:** That's what Nuke is for. It doesn't need a Session — it hunts down every Exit poof has ever made and destroys them.
**Dev:** When I pick a Country, do I get the same Exit as last time?
**Expert:** There is no "last time." Every Session creates a brand-new Exit with brand-new keys. Nothing is reused, nothing is remembered.
