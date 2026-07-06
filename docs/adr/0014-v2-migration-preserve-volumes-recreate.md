# v2 Migration: Preserve Home/Workspace Volumes, Recreate Everything Else

> Status: **superseded — migration complete.** The v1→v2 migration this ADR
> planned is done and v1 is retired; there is a single Sandcastle today (see
> `../topology.md` / `../glossary.md`, history in `../migration-history.md`).
> Retained as a dated decision record. Depends on ADR-0011/0012/0013. Captured 2026-07-01.

Machines are **reproducible** — their only precious state lives on the `sc-home` and `sc-workspace` volumes; the rootfs comes from the image + the project profile. So v1→v2 migration **preserves those two volumes and recreates everything else** (per-user infra project + sidecar, per-project Incus projects, profiles, machines, CA). No cross-project instance surgery.

## Decision

- **Preserve `sc-home` + `sc-workspace`; recreate the rest.** Do not delete these volumes during v1 teardown.
- **Zero-copy volume reuse (V-a).** v1 stores one `sc-home`/`sc-workspace` pair per tenant, partitioned by project **subdirectory**. In v2, each project's `default` profile mounts its **subdir** of the preserved volumes (`sc-home/<project>` → `/home/<user>`, `sc-workspace/<project>` → `/workspace`). No data copy. (Alternative V-b — copy each subdir into a fresh per-project volume — is cleaner but unnecessary; revisit only if per-project volume isolation is later wanted.)
- **UID reseed.** Re-own the preserved volume subdirs to the v2 project user's pinned UID (2000, chosen to avoid the cloud image's `ubuntu`=1000 collision) via the seed-container idmap dance proven on the `thieso` profile.

## Cutover decisions (settled)

- **Migration driver = R1.** Manual re-onboard + a runbook: users re-run `sc login`, recreate machines; admin re-attaches the preserved volumes; v1 is torn down once v2 is verified. No scripted migrator (R2) for now — add only if users can't self-re-onboard.
- **Hostname cutover = accept the break.** The name changes `<machine>.<project>.<tenant>` → `<machine>.<project>`; existing `known_hosts`/SSH configs/route targets break and are regenerated on re-login (which already rewrites local DNS + known_hosts). No `.tenant` grace-period alias.

## Consequences

- Migration is a **runbook + volume preservation**, not migration code (under R1) — cheapest path, matches the reproducible-machine reality.
- **Per-tenant → per-user CA:** for personal tenants (user == tenant) the CA carries over; leaf certs are re-issued anyway because hostnames change (SANs).
- **Tailnet:** per-tenant → per-user; for personal tenants it's the same tailnet — swap the two old sidecars (CoreDNS + Tailscale) for the one consolidated sidecar.
- **Rollback:** under R1, v1 stays intact until the user confirms v2 works, then v1 is torn down (volumes already preserved).
</content>
