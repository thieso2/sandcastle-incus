# Sandcastle Live Integration Test

Date: 2026-05-22

Target: `https://big.thieso2.dev`

This is a real end-to-end test against the `big` Incus remote. Infrastructure
commands are run from the repository with `.env` loaded. User commands are run
from outside the repository so the local `.env` file is not loaded.

## Commands

```sh
set -a
. ./.env
set +a
VERBOSE=1 ./bin/sc-adm infra delete --purge
VERBOSE=1 ./bin/sc-adm infra create

cd /tmp
VERBOSE=1 /Users/thies/Projects/GitHub/incus-sandcastle/sandcastle-incus/bin/sc login https://big.thieso2.dev
VERBOSE=1 /Users/thies/Projects/GitHub/incus-sandcastle/sandcastle-incus/bin/sc create test
```

## Checks

- Infra delete with purge completes.
- Infra create completes.
- Internal TLS mode installs the infrastructure CA locally during create.
- Login starts without `.env` and completes after browser approval.
- Login sets up tenant DNS, local tenant CA trust, and Tailscale.
- `sc create test` creates a machine in the default tenant.
- DNS resolves for the created machine.
- SSH connects to the created machine.

## Results

- Infra delete/create reached the Auth App through Caddy at
  `https://big.thieso2.dev`.
- CLI Device Login was approved without browser UI through
  `POST /debug/device/approve`; the latest live `/tmp` login code `FL9F-F2C2` was
  approved this way and progressed past browser approval.
- Login from `/tmp` was run with local `SANDCASTLE_TAILSCALE_AUTHKEY`,
  `SANDCASTLE_AUTH_TAILSCALE_AUTHKEY`, and
  `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` explicitly unset. The Auth App supplied
  the configured lab Tailscale auth key to the approved Device Login client.
- macOS tenant CA trust is now cached at
  `~/.config/sandcastle/tenant-ca/sandcastle-thieso2/sc-thieso2` and reused on
  login, avoiding repeated trust prompts for the same recreated tenant.
- `sc tailscale up --dry-run --output json` now renders
  `tailscale up --timeout=60s ...`, so unattended login attempts fail cleanly
  instead of hanging when Tailscale cannot authenticate.
- The tenant sidecar can produce an interactive Tailscale login URL without an
  auth key, but the live integration has no Tailscale API/OAuth credential
  available to mint a replacement unattended key.
- Blocked before `sc create test`: the configured
  `SANDCASTLE_E2E_TAILSCALE_AUTHKEY` is rejected by Tailscale as invalid:
  `invalid key: API key ... not valid`.
