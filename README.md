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
SANDCASTLE_E2E=1 SANDCASTLE_E2E_LOCAL_VM=1 scripts/e2e.sh local-vm
SANDCASTLE_E2E=1 SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_TAILSCALE_AUTHKEY=tskey-auth-... scripts/e2e.sh tailscale
SANDCASTLE_E2E=1 SANDCASTLE_E2E_IMAGE_BUILD=1 SANDCASTLE_E2E_CODEX_VERSION=... SANDCASTLE_E2E_CLAUDE_CODE_VERSION=... SANDCASTLE_E2E_GEMINI_CLI_VERSION=... scripts/e2e.sh images
SANDCASTLE_E2E=1 SANDCASTLE_ROUTE_BROKER_INCUS_SOCKET=/var/lib/incus/unix.socket SANDCASTLE_E2E_BASE_IMAGE_SOURCE=sandcastle/base:debian-13 SANDCASTLE_E2E_AI_IMAGE_SOURCE=sandcastle/ai:debian-13 SANDCASTLE_E2E_PUBLIC_DOMAIN=e2e.example.com SANDCASTLE_E2E_INFRA_HOST=203.0.113.10 SANDCASTLE_E2E_LETSENCRYPT_EMAIL=ops@example.com scripts/e2e.sh public-routes
```

The destructive tiers refuse to run unless their required environment variables
are set. See the e2e testing plan for Incus, image, Tailscale, public route, and
disposable VM requirements.
