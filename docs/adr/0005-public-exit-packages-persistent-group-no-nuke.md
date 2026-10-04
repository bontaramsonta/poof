# Public Exit packages, one persistent security group, no Nuke

A second client, the [poof-android](https://github.com/bontaramsonta/poof-android) control plane, launches Exits on a phone's behalf. Its Exits MUST be interchangeable with the CLI's, so the Exit code is exported: `exit` (Country map, user-data, EC2 launch and terminate) and `wgkey` move out of `internal/`, and both clients build from them. Launch takes extra instance tags (the control plane adds `poof:client=android`); every tag is set inside `RunInstances`, never by a later `CreateTags`, so a least-privilege IAM policy can forbid retagging.

Each Exit used to get its own security group, which was never deleted at teardown and leaked one group per Session. Now every Exit launches into one group per region, `poof-wireguard`, tagged `poof=1`, inbound UDP 51820 only. Launch ensures it: look it up by name, create it if missing, re-add the rule if it was removed. Nothing deletes it.

`poof nuke` is dropped. With a persistent group it would delete the group every client shares, and its cross-region terminate-everything is exactly what the phone's IAM role must never be able to do. The Dead-man's switch ([ADR-0003](0003-deadmans-switch-teardown.md)) is the only backstop for orphaned Exits.

Cross-client invariants (version skew between clients is tolerated while these hold): the `poof=1` tag, the group name `poof-wireguard`, shutdown-means-terminate, and the 5 min switch threshold.
