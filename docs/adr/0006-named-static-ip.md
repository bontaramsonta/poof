# Named Static IP, the one thing that outlives a Session

Some APIs allowlist a caller by source IP; Groww's trading API, for one, binds each API key to a fixed public IP. A fresh Exit gets a fresh IP, so poof could not reach them.

`poof up --static-ip <name> <country>` launches the Exit as usual, then attaches an Elastic IP tagged `poof=1` and `poof:static-ip=<name>`. The first use allocates it, inside `AllocateAddress` so no `CreateTags` is needed. Later Sessions with the same name and Country find it by tag and reuse it. The Exit is still born fresh, keys and all, and still dies with the Session or by the Dead-man's switch; only the address survives. Termination detaches it.

Association uses `AllowReassociation=false`. Launch refuses while a pending or running instance holds the address; a terminating holder is detached first, because EC2 keeps the address on it for a minute or more. Two Sessions MUST NOT share a Static IP, and one MUST NOT steal it from another.

Nothing releases a Static IP implicitly. `poof static-ip release <country> <name>` is the only way, and it refuses while the address is held. An idle Static IP costs the AWS public-IPv4 rate, about $3.60 a month.

Rejected: a long-lived Exit with a stable IP. It breaks the ephemerality promise and needs a second teardown story.

`--only <host,...>` makes the Proxy split: listed hosts and their subdomains go through the Exit, everything else goes direct. It matches on the hostname the client sends, so clients MUST use `socks5h`. It is proxy-mode only; system mode routes by IP, and CDN-fronted APIs do not have stable IPs.
