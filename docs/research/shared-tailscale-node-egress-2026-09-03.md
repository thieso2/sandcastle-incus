# Can tenant machines reach a node SHARED into the tenant tailnet? (2026-09-03)

Question (Thies): terrarium's `lab-edge` (7os tailnet, 100.120.192.99) is
now shared into the moyn tailnet and renumbered there to the same address;
laptops reach it. Can machines inside a sandcastle tenant (not tailnet
nodes; egress via the sidecar, ADR-0026) use that shared node too?

## Observed on tenant thieso2 / obelix, 2026-09-03

- Egress is on (`user.sandcastle.v2.tailnet-egress=true`): the bridge
  carries `dhcp-option=121,100.64.0.0/10,10.123.0.3,0.0.0.0/0,10.123.0.1`,
  the devbox routes `100.120.192.99 via 10.123.0.3`, and the sidecar's
  `sandcastle-egress` nft table masquerades tenant→CGNAT onto tailscale0.
  The steer and NAT work.
- The sidecar, `tagged-devices` / `tag:sandcastle`, answers
  `tailscale ping 100.120.192.99` with **"no matching peer"**, and its
  netmap has no `lab-edge`. A machine's `curl https://viewer.lab.7os.io/`
  dies there.
- Root cause is Tailscale's sharing model, not sandcastle: a shared machine
  is visible **only to the devices of the user who accepted the share**, and
  "a machine cannot be … accessed by tagged machines on another tailnet, as
  only users can accept machine shares" (docs/features/sharing;
  tailscale/tailscale#5321, open since 2022, P2). No ACL, route or NAT
  changes what a tagged node's peer list contains.

So: the sidecar forwards fine; it will never *have* the shared node. Any
integration must put a **user-owned** node in the path.

## Options

### A. Log the tenant sidecar in as the user (no new parts)

`sc tailscale up` already accepts an empty tag list
(`--advertise-tag=` → `NormalizeAdvertiseTags` drops it → plain
`tailscale up --advertise-routes=<cidr> --hostname=… --accept-dns=false`,
interactive login URL). Logged in as `thies@moyn.dev`, the sidecar is one of
Thies's devices, sees every machine shared with him, and the existing
egress steer + NAT deliver machine traffic to it. Zero per-machine config,
zero new code.

Costs, all platform-side and per tenant:
- `sc tailscale down` + `up` is a new node: new tailnet IP unless an admin
  sets it back (locally unique IPv4 allows choosing an unused address).
  Laptops' `sc` remotes are enrolled against the sidecar's tailnet IP; keep
  the IP or re-enroll.
- The `/24` subnet route loses the `tag:sandcastle` autoApprover → approve
  by hand once (and after any re-login).
- User-owned nodes expire: disable key expiry for the sidecar.
- Every machine of the tenant acts on the tailnet **as that user**, for all
  destinations egress allows (ADR-0026 accepted this for the tag; for a
  user it is stronger). Acceptable for a personal tenant (thieso2 = Thies's
  own machines); not for a shared tenant like skorfmann's prod project.
- `sc` tooling has no other tag assumption (grep: only docs and the e2e
  env), so the CLI keeps working.

### B. A user-owned egress node for one address (platform feature)

Keep the sidecar tagged. Add a tiny CT in the tenant, logged in as the user,
that sees the shared node, and steer only `100.120.192.99/32` at it:
option 121 carries several routes, so the bridge value becomes
`…,100.120.192.99/32,<ct>,100.64.0.0/10,<sidecar>,0.0.0.0/0,<gw>`, and the
CT masquerades that one destination onto its tailscale0 (the eike pattern
from moyn-dev/instance, on the moyn tailnet this time). The user identity is
then confined to one destination; the sidecar keeps its tag and autoApprover.

Needs sandcastle work: per-tenant "extra egress routes" (a project config
key read by `CreateTenantV2` next to the egress toggle, e.g.
`sc tailscale egress route add <cidr> via <machine>`), because the converge
rewrites `raw.dnsmasq` on every `sc login`; a hand-edited bridge value
does not survive. Plus the CT itself (image with tailscaled, identity
volume, nft), which is the router role that moyn-dev/instance just retired.

### C. Per-machine Tailscale client (the ADR's deferred opt-in)

A tailscaled inside the machine, logged in as the user: the machine sees
the share directly. Simplest for one box; the ADR rejected it as the
default (device sprawl, dual reachability). Fine as an explicit opt-in for
the few machines that need the lab.

### D. Wait for Tailscale

tailscale#5321 would let tagged nodes use shared nodes. Open, no maintainer
commitment. Not a plan.

## Recommendation

- thieso2 (Thies's own tenant, the dev-env this was all for): **A**. Two
  commands, one approval, one expiry toggle, set the IP back. Everything
  else is already in place and proven up to the peer-list check.
- Any tenant whose machines belong to several people: **B** if the need
  recurs, otherwise **C** per machine. Do not log a shared tenant's sidecar
  in as one person.

Verification for A, from a tenant machine: `ip route get 100.120.192.99`
(next hop the sidecar, already true), `curl -sI https://viewer.lab.7os.io/`
→ 200 with the `*.lab.7os.io` certificate; on the sidecar
`tailscale status | grep lab-edge`.

Open point to test once A is done: the 1:1 NAT for shared nodes happens in
the recipient client "right before packets flow into the WireGuard tunnel"
(tailscale.com/blog/choose-your-ip). Traffic the sidecar forwards and
masquerades also enters that tunnel, so it should be translated the same
way; confirm with the curl above.
