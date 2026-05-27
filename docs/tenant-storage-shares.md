# Tenant Storage Shares

Tenant Storage Shares let one tenant expose a workspace subdirectory to explicit recipient tenants. The source tenant keeps normal read/write access through `/workspace`; recipient tenants see accepted shares read-only under `/shared/<source-tenant>/<source-project>/<share-name>`.

## Scope

- Shares are same-deployment only.
- Shares are workspace-only and directory-only.
- The source is an explicit project plus workspace-relative directory, for example `default:docs`.
- `/workspace` itself is not shareable; the source must be below the project workspace root.
- The canonical share identity is `(source tenant, source project, share name)`.
- Share name and source directory are immutable after creation.
- Recipient membership is mutable.
- Share creation requires at least one recipient tenant.
- Recipient tenants must exist before they can receive a share offer.
- Source tenants cannot offer shares to themselves.
- Recipients cannot rename inbound shares locally.
- Inbound shares cannot be re-shared.
- Initial implementation supports container-backed machines only.
- Tenant infrastructure sidecars do not receive share mounts.

## Lifecycle

- `create`: a source tenant user creates a share and offers it to one or more recipient tenants.
- `accept`: a recipient tenant user accepts an offered share for that tenant.
- `decline`: a recipient tenant user removes or rejects local visibility while the source offer remains active.
- `revoke`: a source tenant user removes one recipient tenant from a share.
- `delete`: a source tenant user deletes the whole share, all recipient offers and acceptances, and any remaining recipient mounts.
- `reconcile`: Sandcastle reapplies accepted share state to recipient machines after drift or partial failure.

Offers do not expire. Declined offers can be accepted later while the source offer remains active. Revoking the last recipient is rejected; delete the share instead. Deleting a source tenant deletes its outbound shares. Deleting a recipient tenant removes its inbound acceptances.

Active recipient states are `pending`, `accepted`, and `declined`. Availability is tracked separately as `available` or `unavailable`, because an accepted share can become unavailable. `revoked` is audit history, not an active recipient state.

## Visibility And Safety

Accepted shares are visible to all current and future machines in the recipient tenant. All `/shared` mounts are read-only. Share data belongs to the source tenant's storage and counts against the source tenant's storage ownership.

Share creation requires the source directory to exist. If the source directory later disappears or becomes boundary-unsafe, the share remains defined but becomes unavailable. Unavailable shares are shown in share status output and are removed from recipient machines; no placeholder path is left behind.

When an unavailable source becomes available and safe again, reconciliation restores mounts for accepted recipients automatically.

The shared directory tree is exposed as-is, including dotfiles, but symlink traversal must not expose paths outside the source directory. Reconciliation must continue checking this boundary because a safe directory can become unsafe later.

## Authorization And Control Plane

Any user with Tenant Access to the source tenant may create, revoke, or delete its shares. Any user with Tenant Access to a recipient tenant may accept or decline shares offered to that tenant. Acceptance is recorded for the recipient tenant, not for the individual user, with the acting user kept as audit metadata.

Share operations use the Auth App identity model and CLI Auth Token. A privileged share broker in the Auth App authorizes requests against Tenant Access, updates share state, and reconciles read-only machine mounts. Restricted user Incus credentials do not directly attach another tenant's storage.

Source offer and revocation state lives with the source tenant. Recipient acceptance and visibility state lives with the recipient tenant so each tenant can reconcile its own machines from local desired state.

Tenant metadata is the v1 source of truth for share state. The Auth Database should not keep a second authoritative or query-index copy unless listing performance later requires a derived index.

For v1, share metadata should live in the existing tenant metadata area rather than a new dedicated custom volume.

## CLI Shape

Planned user commands:

```text
sc share create <project>:/workspace/<dir> --to <tenant> [--to <tenant> ...] [--name <share-name>]
sc share list [--outbound] [--inbound] [--offers]
sc share status <project>/<share-name> [--verbose]
sc share offers
sc share accept <source-tenant>/<source-project>/<share-name> [--dry-run]
sc share decline <source-tenant>/<source-project>/<share-name> [--dry-run]
sc share revoke <project>/<share-name> --tenant <recipient-tenant> [--dry-run]
sc share delete <project>/<share-name> [--yes] [--dry-run]
sc share reconcile [--tenant <tenant>] [--dry-run]
```

The create command may accept `/workspace/docs` or `docs` as input and normalizes both to a workspace-relative source path. If the source directory basename is not a valid path-safe share name, `--name` is required.

Tenant status should include share summary counts: outbound shares, inbound accepted shares, pending inbound offers, and unreconciled machines. Detailed share rows belong in `sc share list` and `sc share status`.

Mutating commands should support `--dry-run --output json` plans. Reconciliation is convergent: desired share state is recorded first, machine-level changes are applied as far as possible, and per-machine failures are reported for retry. Reconciliation should not automatically stop or restart machines.

Reconciliation runs synchronously in v1 and returns per-machine results to the caller. Background jobs can be added later if share operations become too slow.

A mutating command succeeds when authorization, validation, and desired-state recording succeed. Per-machine reconciliation failures are reported in the command result for later retry rather than making the state change fail.

`sc share reconcile` exits non-zero when any targeted machine remains unreconciled.

## Mount Implementation

Accepted shares should be applied as deterministic per-machine disk devices, not through tenant profiles. Device names should be short, stable, and collision-resistant, while human-readable source and target paths stay in Sandcastle metadata and CLI output.

For recipient machines, devices mount the source tenant's workspace subdirectory read-only at `/shared/<source-tenant>/<source-project>/<share-name>`.
