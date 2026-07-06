# Machine Private Hostnames: `<machine>.<project>.<suffix>`, Zone-Only Authority

> Status: **accepted** (2026-07-06). Amends ADR-0016 decision 9 (flat `<machine>.<suffix>`) and finally delivers the project-qualified naming ADR-0012 wanted, without its per-project zones.

Flat per-tenant DNS collides the moment two projects reuse a machine name (`test2:dev` and `test3:dev` both claim `dev.<suffix>`, first-wins), and the v2 reconciler only registered the `default` project at all — machines elsewhere resolved, if ever, through the bridge dnsmasq fallthrough by accident. We replace this with a canonical, deterministic naming contract.

## Decisions

1. **Canonical name — the Machine Private Hostname — is `<machine>.<project>.<Tenant DNS Suffix>`** for every machine in every app project, including `default`. Wildcard `*.<machine>.<project>.<suffix>` accompanies each record (per-machine app subdomains).

2. **Exactly one short form: the Default Project Short Hostname.** `<machine>.<suffix>` (+ wildcard) aliases the *default project's* machine of that name. Machines in other projects have **no** short name — never first-wins, never uniqueness-dependent (records must not appear or vanish because an unrelated project reused a name).

3. **The Tenant DNS Suffix is tenant-chosen, single-label, set at tenant creation, immutable.** `--dns-suffix` on `sc-adm tenant create` and on `sc login` (first-login provisioning); default remains the tenant name. Existing `domainrules` validation applies (public-TLD and IANA special-use denied unless allowlisted, admin allow/deny lists). With BYO tailnets a suffix only exists inside the tenant's own split DNS, so two tenants may pick the same word. Multi-label suffixes are a possible future loosening, not blocked by anything here.

4. **The sidecar CoreDNS zone is the only DNS authority — the dnsmasq fallthrough is removed.** Lease names are guest-asserted (spoofable within the tenant) and single-label (cannot express the project), so DHCP carries no naming authority. Guests still *know* their canonical name: the per-project profile stamps `fqdn: <name>.<project>.<suffix>` via jinja-templated cloud-init — identity, not resolution.

5. **Registration is event-driven with a periodic safety net.** The auth-app subscribes to Incus lifecycle events (all projects), waits bounded for the new instance's tenant-bridge IPv4, and rewrites only that tenant's zone — records exist within seconds, typically before `sc create` returns. The existing 30s reconcile loop remains for missed events, restarts, and cleanup drift. Events buy latency; the loop guarantees convergence.

6. **e2e exercises the full stack.** `scripts/e2e-v2.sh` deploys the real auth-app (simulated GitHub, no public ingress) and asserts resolution through the genuine event-driven registration path — explicitly no harness-only reconcile shortcut. Negative assertion: a non-default machine's short name must NOT resolve.

## Considered and rejected

- **Short name when unique across projects** — spooky action at a distance (rejected under decision 2).
- **DHCP-lease/dnsmasq-based naming** (`dns.mode=dynamic`, client FQDN options) — guest-controlled and label-limited (rejected under decision 4).
- **A one-shot `sc-adm dns reconcile` for e2e** — rejected as a harness shortcut; may still appear later as an ops tool, but e2e must not depend on it.
