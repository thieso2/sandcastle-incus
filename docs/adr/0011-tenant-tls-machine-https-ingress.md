# Tenant TLS and Machine HTTPS Ingress

> **Status: ACCEPTED** (design grilled; ready for implementation).

Sandcastle machines terminate HTTPS locally with a `caddy` profile: Caddy on the
machine serves valid TLS for the machine's own DNS names, force-redirects
HTTP→HTTPS, and reverse-proxies the tenant's app. Certificates are issued by the
**per-tenant CA**, whose private key lives only on the tenant's sidecar; the
machine never holds a signing key.

## Locked decisions

1. **One CA per tenant.** The existing per-tenant CA
   (`certs.GenerateCA("Sandcastle <tenant> tenant CA")`, generated in
   `PlanCreateV2`) is the trust root. No install-wide/global root — a global
   root would let one tenant's machine forge a browser-trusted cert for another
   tenant, collapsing the tenant boundary (ADR-0001, ADR-0006).

2. **The sidecar is the signer; the CA key never leaves it.** Machines get
   per-machine leaf certificates; Caddy fetches its cert and never signs. This
   caps the blast radius of a compromised machine to *no* signing capability.

3. **No per-machine authorization; the sidecar signs anything in its own zone.**
   Any machine in a tenant may obtain a cert for any name under that tenant's
   zone (`*.default.<suffix>`). Blast radius is therefore *within one tenant*
   (same user, same trust domain) — accepted in exchange for zero
   authorization machinery.

4. **Trust distribution: the tenant CA is trusted on both the Mac client and the
   tenant machines.** On the Mac it is installed into the **system keychain**
   (`sudo` at `sc login`, accepted), so all browsers/apps trust
   `https://*.default.<suffix>`. The machines also trust it (the profile already
   stages `/ca.crt`) for machine-to-machine HTTPS.

5. **The CA is NOT name-constrained — a knowing acceptance.** In a single-user,
   self-hosted dev model the sidecar (and thus the CA key) is operator-owned, so
   an unconstrained root in one's own trust store has no adversary who benefits.
   Name-constraining to `default.<suffix>` remains a cheap, reversible hedge if
   the model ever becomes multi-user; revisit then.

6. **Cert delivery: fetch-at-first-boot from a sidecar signing endpoint,
   long-lived.** The machine's cloud-init obtains a leaf with SANs
   `[<machine>.<project>.<suffix>, *.<machine>.<project>.<suffix>]` from the
   sidecar (which holds the tenant CA key) BEFORE Caddy starts, so ordering is
   guaranteed and nothing is generated-then-pushed. No ACME / step-ca / DNS-01:
   the wildcard is trivial because the endpoint just signs (no challenge).
   Lifetime is **long (~10y, ≤ CA validity)**; re-fetched on machine recreate.
   (Supersedes the earlier push-at-create option — with Caddy in the machine's
   cloud-init, fetch keeps everything machine-side self-contained and avoids a
   boot race, in exchange for a signing service on the sidecar.)

## Routing / file-server decisions (locked)

7. **Caddy runs as root; routes are open (no auth).** `/_r` → `file_server
   browse` rooted at the machine's real `/`; `/_w` → `/workspace`; everything
   else reverse-proxies to `localhost:3000`; unconditional HTTP→HTTPS. Justified
   by the single-owner model. **Known acceptance:** `/_r` at `/` exposes the
   machine's entire filesystem — including the pushed leaf private key and SSH
   host keys — read-only to every device on the tenant's tailnet.

   **Impl note:** Caddy's `file_server browse` cannot list the filesystem root
   when its root is literally `/` (404 on the bare root listing; files and
   subdirs are fine). So `/_r` roots at a **bind mount of `/`**
   (`/run/sandcastle-rootfs`, recreated on boot by a systemd oneshot ordered
   before Caddy), and `redir /_r /_r/` handles the bare path.

8. **No separate profile — extend the existing v2 default profile's cloud-init.**
   `V2DefaultProfileUserData` (already `## template: jinja`) gains the Caddy
   install + Caddyfile, rendering the per-machine site address from
   `{{ v1.local_hostname }}.<project>.<suffix>`. Being one user-data document
   avoids Incus's last-profile-wins clobber of `cloud-init.user-data`.
   **Consequence:** every tenant machine gets Caddy by default (not opt-in).

9. **The signer is the Sandcastle binary on the sidecar** (`sidecar tls-sign`
   HTTP service), unauthenticated, reachable only on the tenant bridge. It reuses
   the `certs` package to sign leaves with correct SANs incl. the wildcard.
   **Precedent:** first Sandcastle code to run on a sidecar (previously stock
   Debian + CoreDNS + Tailscale) — sidecar setup now ships + version-couples the
   binary and a systemd unit. The machine discovers it at a fixed URL on its
   nameserver (`.3`).

10. **Cert delivery detail: keygen-on-sidecar (no CSR).** The `tls-sign` endpoint
    mints key+cert and returns both; the machine writes them and starts Caddy.
    Simplest on both ends. The leaf key crosses the tenant bridge in cleartext
    HTTP — accepted because it is strictly less exposure than decision 7 (which
    already serves that key over `/_r` to the whole tailnet). CSR (key never
    leaves the machine) was rejected as more code for a property already given
    away.

11. **Wildcard routes to the app.** Caddy's site line is
    `<machine>.<project>.<suffix>, *.<machine>.<project>.<suffix>`, both reverse-
    proxied to `localhost:3000`; the app vhosts on `Host`. `reverse_proxy`
    preserves the incoming `Host` by default — no `header_up Host` override, or
    subdomains are lost. `/_r` and `/_w` take precedence over the proxy; all
    `/_…` is reserved for Sandcastle.

## Resulting shape

- **Sidecar:** runs the Sandcastle binary as an unauthenticated `sidecar
  tls-sign` HTTP service on the tenant bridge, signing leaves with the tenant CA.
- **`sc login`:** installs the tenant CA (unconstrained) into the Mac system
  keychain via `sudo`.
- **Machine (default profile cloud-init, jinja):** installs Caddy; fetches its
  leaf (key+cert) from the sidecar before starting Caddy; runs Caddy as root
  with a site that force-redirects HTTP→HTTPS, serves `/_r`→`/` and
  `/_w`→`/workspace` via `file_server browse`, and reverse-proxies everything
  else (Host preserved) to `localhost:3000`.

## Implementation notes (post-audit)

Primitives already exist: `certs.IssueMachineLeaf`, `certs.MachineDNSNames`,
and `localtrust` (platform trust store — installs a CA into the macOS keychain,
idempotent). Two adjustments to the ADR from what the code actually does:

- **Trust keychain:** the existing `localtrust` darwin store targets the
  **login keychain (no sudo)**, which is strictly nicer than the ADR's
  system-keychain choice. Keep it (system keychain remains available via
  `SANDCASTLE_DARWIN_TRUST_KEYCHAIN`). Supersedes decision 4's sudo note.
- **CA storage in v2:** the signer **self-generates** the tenant CA on first
  start (`/etc/sandcastle/ca/{ca.crt,ca.key}`) if absent — the key lives only on
  the sidecar and everyone fetches the cert from `/tls/ca`, so nothing is pushed
  and the CA stays stable across re-provisions. The CN is **suffix-scoped**
  (`Sandcastle <suffix> tenant CA`) so two installs sharing a tenant name get
  distinct roots; the client names its trust entry after the fetched CA's CN.

Build order: (1) `internal/tlssign` handler + `sandcastle-admin sidecar tls-sign`
command (self-inits CA); (2) sidecar setup ships binary + systemd unit; (3) v2
login installs trust by fetching `ca.crt` from the signer; (4) default-profile
cloud-init installs Caddy + fetches its leaf. Endpoint: `GET /tls/ca`,
`GET /tls/leaf?fqdn=<name>` → `{cert,key}`, on the sidecar bridge IP.

**Verified end-to-end** (idefix `ct2`): signer self-generated
`Sandcastle idefix tenant CA`; the machine fetched a leaf with SANs
`[ct2.default.idefix, *.ct2.default.idefix]`; Caddy (root) served valid HTTPS
chained to the CA (no `-k`), redirected HTTP→HTTPS (308), proxied to `:3000`,
vhosted the wildcard subdomain, and `/_r` browsed `/` while `/_w` browsed
`/workspace`.
