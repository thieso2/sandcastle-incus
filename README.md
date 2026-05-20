# Sandcastle Incus

Sandcastle Incus is the Incus-backed implementation of Sandcastle v1.

The product CLI is `sandcastle`, with `sc` installed as an alias. The system
creates project-scoped Incus sandboxes for AI and development workflows, connects
them to a user-owned Tailscale network, serves private project DNS, and can
publish HTTP routes through a shared infrastructure Caddy.

## Documents

- [Sandcastle v1 specification](docs/sandcastle-v1-spec.md)
- [Implementation plan](docs/implementation-plan.md)
- [End-to-end testing plan](docs/e2e-testing-plan.md)

