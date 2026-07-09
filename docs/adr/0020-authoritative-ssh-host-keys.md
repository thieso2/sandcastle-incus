# Authoritative SSH Host Keys in `~/.ssh/known_hosts`, Marked and Reclaimable

> Status: **accepted** (2026-07-09). Replaces the per-tenant known_hosts file and the `ssh-keyscan` trust-on-first-use path for v2 machines. Builds on ADR-0018 (Machine Private Hostnames).

Every v2 machine regenerates its SSH host keys on first boot (cloud-init runs `ssh-keygen -A` after `rm -f /etc/ssh/ssh_host_*`). Recreate a machine and its host key changes. Meanwhile `sc connect` dialled the machine's private IP with `StrictHostKeyChecking=accept-new`, so OpenSSH recorded an **IP-keyed, untagged** line in `~/.ssh/known_hosts` — and private IPs are recycled DHCP leases. The result was a file that accumulated one dead line per IP ever touched (100 of them on the author's laptop), and a `ssh <machine>.<project>.<suffix>` that failed with `REMOTE HOST IDENTIFICATION HAS CHANGED` after any rebuild. `accept-new` also cannot distinguish a rebuilt machine from an impostor; it simply trusts whoever answers port 22 first.

## Decisions

1. **The host key is read authoritatively over the Incus API, never scanned from the network.** `GetInstanceFile` pulls `/etc/ssh/ssh_host_*_key.pub` out of the instance across the mTLS Incus connection — the same channel `localtrust` already uses for certificates. The key never traverses the network the SSH session is about to cross. Verified to work under a **restricted tenant certificate**, not just admin certs.

2. **`sc connect` therefore runs `StrictHostKeyChecking=yes`.** Knowing the true key before dialling is what makes strict checking possible: a rebuilt machine never trips the warning, and a genuine impostor now always does. `accept-new` could do neither.

3. **Entries are keyed by name, via `-o HostKeyAlias`, never by IP.** Connect still dials the private IP (no dependency on the local resolver), but the known_hosts lookup uses the Machine Private Hostname. Names are stable; leases are not. One line carries both names ADR-0018 defines: `<machine>.<project>.<suffix>,<machine>.<suffix>`. No IP-keyed line is ever written again.

4. **`~/.ssh/known_hosts` is the single source of truth; the per-tenant file is abandoned.** `sc` and a bare `ssh` must agree, and they can only agree if they read the same file. (v1's `~/.config/sandcastle/<remote>/known_hosts/<tenant>` remains for v1 machines and dies with v1.)

5. **Every line Sandcastle writes carries a `# sandcastle:<remote>/<tenant>` marker**, plus ` tofu` when the key was not authoritative. The marker is what makes deletion safe: we delete lines we wrote, and reclaim names we own. A line the user wrote is never removed for merely looking like ours.

6. **All of a machine's host keys are recorded, not just the strongest.** OpenSSH's `UpdateHostKeys` (default on) learns a server's other host keys after authenticating and appends them to known_hosts. Recording only ed25519 means the next bare `ssh` adds an untagged `ssh-rsa` line, which the next connect reclaims and deletes, which ssh then re-adds — a permanent ping-pong. Recording all of them makes the file converge and connect idempotent.

7. **Two bounded exceptions delete lines Sandcastle did not write.**
   - **Name reclamation** (on every connect): an untagged entry claiming a name we own is removed. Without this the stale line still wins, because OpenSSH uses the *first* match. This is what fixes the original failure.
   - **Recycled-IP purge** (on every connect): an untagged entry whose host is a literal IP inside the tenant's private CIDR is removed. Nothing writes IP-keyed entries any more, so such a line has no lasting owner. Hashed (`|1|`) entries are found by HMAC-probing the CIDR's addresses; hashed *name* entries cannot be reversed and are never guessed at.

   Everything else — `@cert-authority`, `@revoked`, comments, wildcard patterns, foreign names — is copied through untouched. A wildcard that would shadow one of our names is warned about, never deleted.

8. **The tenant CIDR is read from a live machine's own interface, not from tenant metadata.** A restricted tenant certificate cannot see the tenant bridge's `ipv4.address` (Incus redacts network config), and `tenant.Summary.PrivateCIDR` is empty for v2. `GetInstanceState` reports the machine's address *and netmask*, which is authoritative and needs no infra-project visibility. With no running machine to ask, the IP purge is skipped and says so.

9. **Guardrails, because the file is the user's.** A snapshot to `~/.ssh/known_hosts.sc-backup-<date>` precedes the first destructive write of the day; every removal is printed; a tenant CIDR shorter than `/22` (>1022 addresses, i.e. a misconfigured `SANDCASTLE_CIDR_POOL`) disables the IP purge and reports rather than deleting. Rewrites are atomic (temp + rename) under an `flock`, so concurrent `sc` processes cannot lose each other's edits.

10. **`sc ssh-key purge` is the maintenance verb.** Connect self-heals the machine it is connecting to; only purge has the live-machine list needed to identify **tagged orphans** (lines for machines that no longer exist). It reconciles every live machine, drops orphans and IP debris, prints a plan, and asks before applying (`--yes`, `--dry-run`, `--all`).

11. **`ssh-keyscan` survives only as a fallback, and says so.** `GetInstanceFile` reaches a container's filesystem directly but a virtual machine's only through `incus-agent`; a VM whose agent is down cannot be read. Those keys are scanned, tagged ` tofu`, and connect proceeds. Any later connect that *can* read the machine rewrites the line and drops the marker. This never bricks a connect, and never claims a guessed key was verified.

## Considered and rejected

- **A `sc c --fix` flag.** Once connect self-heals unconditionally, a flag to "also fix it" is a flag to do what already happened. Maintenance that needs no target machine belongs in its own verb (decision 10).
- **Keeping `ssh-keyscan` as the trust root.** A repair mode that deletes a mismatched key and re-scans is strictly worse than the status quo: it disarms the MITM warning and then trusts the wire. Rejected under decision 1.
- **Keeping the per-tenant known_hosts file, or mirroring into both.** Isolation is real but the sync bugs are exactly the bugs being fixed, and a bare `ssh` reads neither mirror. Rejected under decision 4.
- **Purging by DNS suffix as well as by CIDR.** `ValidateTenantDNSSuffix` permits a public TLD when an admin allowlists one; a tenant with suffix `dev` would then have `sc` delete every `*.dev` line in the user's file. Name deletion is restricted to tagged lines and to names of live machines (decision 7).
- **Deriving the tenant CIDR from the machine IP plus an assumed `/24`.** `DefaultTenantPrefixBits` is configurable; assuming it would over-purge. The machine reports its own netmask (decision 8).
- **Setting `UpdateHostKeys=no` on `sc`'s ssh invocation.** It would stop `sc` from creating the untagged lines, but not a bare `ssh` — the very client this ADR exists to serve. Rejected under decision 6.
