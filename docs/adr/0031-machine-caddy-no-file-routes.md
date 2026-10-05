# ADR-0031: Machine Caddy serves no file routes, runs as caddy, and leaves an owned or stopped Caddy alone

Date: 2026-10-05. Status: accepted. **Amends** ADR-0011 decisions 7 and 11
(file routes, Caddy as root) and the `--refresh` behaviour of ADR-0028 /
spec machine-hostnames §6.

## Context

ADR-0011 gave every machine's Caddy two open file routes: `/_h` browsed the
login user's `$HOME` and `/_w` browsed `/workspace`. Caddy ran as root so it
could read every file under `$HOME`. The ADR justified both by the
single-owner model and accepted that every device on the tenant tailnet could
read the home directory.

That trade no longer holds. Shared tenants (ADR-0029), subnet routes and
public names put more clients in reach of a machine's `:443` than its owner's
own devices, and a home directory holds the secrets a developer machine
needs: SSH keys, CLI logins, cloud tokens. Running as root meant mode 0600
did not protect them.

`caddy-setup --refresh` made it worse to opt out by hand. The Auth App runs
it after every hostname or certificate push. It rewrote the Caddyfile
whatever was there and started Caddy when it was not running, so a machine
that had stopped Caddy or written its own Caddyfile got the platform
Caddyfile back and Caddy running again.

## Decision

1. **No file routes.** Every site block caddy-setup renders has one handler:
   `reverse_proxy localhost:3000`, `Host` preserved. `/_h`, `/_w` and their
   redirects are gone. `/_…` stays reserved for Sandcastle. A machine that
   wants a file browser runs one behind its own app on `:3000`, with its own
   auth.
2. **Caddy runs as `caddy`.** The drop-in at
   `/etc/systemd/system/caddy.service.d/override.conf` sets `User=caddy`,
   `Group=caddy` and `AmbientCapabilities=CAP_NET_BIND_SERVICE` (enough to
   bind `:443`), and keeps the platform launcher as `ExecStart`. Every key a
   rendered site block uses (the private leaf and each public name's key) is
   set to `root:caddy`, mode 0640, at each render. Keys stay unreadable to
   other users.
3. **`--refresh` never starts Caddy.** It reloads a running Caddy (restarts
   it when the drop-in changed) and does nothing to a stopped, disabled or
   masked one. First boot still enables and starts Caddy.
4. **`--refresh` replaces only the platform's old drop-in.** If
   `override.conf` is byte-for-byte the drop-in older payloads wrote (Caddy
   as root), refresh replaces it with the new one, runs
   `systemctl daemon-reload` and restarts a running Caddy. Any other content
   is the operator's and is left alone. This is how existing machines move
   off root: the next Auth App push or `sc fix` refresh does it.
5. **The Caddy Owned Marker.** If `/etc/sandcastle/caddy.owned` exists (any
   content), the machine owns `/etc/caddy` and the caddy unit. caddy-setup,
   first boot and `--refresh` alike, then renders no Caddyfile, writes no
   drop-in, and never enables, starts, restarts or reloads Caddy. It still
   writes the Caddy Setup Marker (`PRIVATE=` and `RENDERED=` only), so the
   Auth App keeps pushing certificates to `/etc/sandcastle/tls/<host>/` for
   the machine's own Caddyfile to use. To hand Caddy back, delete the file
   and run `sandcastle-caddy-setup --refresh`.

A marker file was chosen over "skip any Caddyfile without the platform's
header line": platform Caddyfiles rendered before this change carry no such
header, so a header check would have frozen the old file routes on every
existing machine.

## Consequences

- This removes behaviour, which CLAUDE.md's additive rule normally forbids.
  It is a deliberate exception: an opt-in for unauthenticated reads of a home
  directory is not a safe default to keep.
- Existing machines lose the file routes at their next refresh (an Auth App
  push, or `sc fix` running the caddy-publications fixup). Until then they
  serve the old Caddyfile. Operators should run the refresh on machines they
  care about, and treat what sat under `$HOME` as possibly read.
- A machine whose operator stopped Caddy stays stopped; `sc fix --check`
  still reports it as `INACTIVE`.
- `HOME` stays in `machine.env` for other consumers; caddy-setup no longer
  reads it.

## Rejected alternatives

- **Keep the routes behind an opt-in flag.** Still unauthenticated once on,
  and one more contract to carry. A machine that wants file browsing can run
  it on `:3000` with real auth.
- **Keep root and drop only the routes.** Caddy then needs no root at all;
  running a network server as root for nothing widens any future bug.
