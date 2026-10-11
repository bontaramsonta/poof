# Owner, a tag that keeps two people apart by convention

A second person now runs poof in the same AWS account, for a Groww Static IP during development. Without an Owner, his `poof up india --static-ip groww` would borrow the first person's address, and his `poof static-ip release india groww` would give it back to AWS.

Every Exit and Static IP carries `poof:owner=<name>`, set inside `RunInstances` or `AllocateAddress`. The CLI reads the Owner from `owner = <name>` in `~/.config/poof/config`, overridden by `--owner`. With no Owner set, `poof up` and `poof static-ip release` refuse; listings still work.

Static IP names are scoped per Owner and Country. Lookup filters on `poof:owner` too, and the `Name` tag is `poof-static-<owner>-<name>`, so two Owners can each have a `groww` in India. `poof static-ip` lists your own addresses; `--all` lists everyone's with an owner column.

A resource without `poof:owner` is unowned. The CLI refuses to borrow or release it until `poof static-ip claim <country> <name>` tags it. That is the one place poof calls `CreateTags`: retagging an existing address is the point. Claiming keeps the address, so an allowlist that names it stays valid.

On Exits the tag is attribution only. A Session terminates only the Exit it launched, and there is no sweep command, so no CLI behavior depends on it.

The Owner is a convention, not access control. Both people hold `AdministratorAccess`, so IAM cannot stop either one from touching the other's resources. The tag guards against accidents, such as a fresh install acting on someone else's address.

The security group `poof-wireguard` carries no Owner. It opens only UDP 51820 and is never deleted, so sharing it costs nothing.

**Considered Options**

- Derive the Owner from `sts:GetCallerIdentity` (rejected: an explicit setting is visible and does not change when the AWS profile does).
- Fall back to unowned resources when no Owner is set (rejected: a forgotten config on a second machine would act on the first person's Static IP, the accident this exists to prevent).
- Static IP names global per Country, first come first served (rejected: both people want `groww`).
- An ABAC permission set conditioned on `aws:PrincipalTag/poof:owner`, or a separate account per person (rejected for now: the second person keeps admin for other work in the account).

**Consequences**

- Upgrading needs one `poof static-ip claim` per existing Static IP.
- An Owner name follows the Static IP name rule: lowercase letters, digits and dashes. poof-android's control plane uses the same names for phone Exits.
