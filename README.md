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

## Testing

Run the normal test suite:

```bash
go test ./...
```

Run reproducible e2e tiers through the checked-in runner:

```bash
scripts/e2e.sh unit
scripts/e2e.sh gated
scripts/e2e.sh local
SANDCASTLE_E2E=1 scripts/e2e.sh incus
```

The destructive tiers refuse to run unless `SANDCASTLE_E2E=1` is set. See the
e2e testing plan for required Incus, image, Tailscale, and disposable VM
environment variables.
