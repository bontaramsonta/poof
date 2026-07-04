# System-wide mode: utun device + split-default routing on macOS

`poof up --system` routes the whole machine through the Exit, the utun backend deferred in [ADR-0001](0001-userspace-socks-proxy-not-system-vpn.md). It needs root and mutates OS state, so several non-obvious choices are worth recording.

**Split-default routing, not replacing the default route.** Instead of deleting the system's default route, we add two more-specific routes — `0.0.0.0/1` and `128.0.0.0/1` via the utun interface — which together cover the whole address space and win by longest-prefix match. The original default route is never touched, so teardown is just deleting our two routes and the machine's networking snaps back even if we never got to "restore" it. This is the same trick wg-quick and most VPN clients use.

**Pinning the Exit's public IP to the physical gateway.** The WireGuard engine sends encrypted UDP to the Exit's public IP. Once the default-override routes are in place, those packets would themselves match `0.0.0.0/1` and try to go *back into the tunnel* — a loop that black-holes the connection. So before adding the overrides we add a host route for the Exit's IP via the *original* default gateway (captured up front), keeping the encrypted carrier traffic on the physical path.

**Teardown is best-effort and LIFO, because a partial teardown breaks the user's network.** Every mutation registers an undo; Close runs them in reverse, continues past failures, and reports everything that didn't reverse. DNS is the sharp edge: `networksetup` DNS changes **persist across reboot**, so a failed DNS restore isn't self-healing — hence the explicit warning and the saved-then-restored original servers.

**Root via `sudo`, with an env-preservation hint.** The whole process runs as root (utun creation, `route`, `networksetup` all need it). Because sudo resets `HOME`/`AWS_PROFILE`, AWS SSO credential lookup would break; the CLI detects non-root use of `--system` and prints a `sudo --preserve-env=AWS_PROFILE,HOME,AWS_REGION` recipe. Rejected alternatives: a privileged helper binary (more machinery than a weekend tool warrants) and a launchd daemon (persistent state, against poof's zero-state design).

**Scope: macOS only.** The routing/DNS mechanics are platform-specific; `NewSystem` is stubbed to an error on other platforms. Linux (`ip route` + `resolv.conf`/`systemd-resolved`) is a future addition behind the same `SystemTunnel` interface.
