# Image Builder Appliance

Sandcastle Images (the Base Image and AI Image) are built inside a dedicated, admin-managed **Image Builder** appliance and published to an external **Image Registry** (GHCR), from which the Incus host pulls and caches them for Machine creation. The Image Builder is an infrastructure appliance, **not** a Machine, and is driven by `sandcastle-admin image build-remote <base|ai|all>`.

## Decision

- **Engine: rootless podman**, not a docker daemon. The build path already allows `podman` (`internal/images` validates the build tool against `{docker, podman}`) and the Dockerfiles are engine-agnostic. Rootless + daemonless suits a nested, internet-facing box that handles a registry push token, and pairs with `fuse-overlayfs` so docker's overlay-over-ZFS friction never arises on the ZFS-backed host.
- **Isolation: unprivileged Incus container** with `security.nesting: true` and `/dev/fuse` exposed. Not privileged, not a VM.
- **Placement: a dedicated `sc-build` Incus project** on the `incusbr0` bridge (which has outbound NAT, proven by Caddy ACME). Kept separate from the Infrastructure Project so it does not couple to `infra create`'s three-sidecar contract (ADR-0007) and so its distinct security profile is isolated.
- **Self-independence: the appliance runs neutral `images:debian/13`**, not the Base Image it produces, with podman/fuse-overlayfs/passt + subuid and `/dev/fuse` installed at provision time. It depends on no Sandcastle Image, so it can always rebuild even if every Sandcastle Image is broken.
- **Provenance: working-tree context** shipped from the operator's Mac (consistent with the local build path), stamped via `git describe --dirty`, with a `--require-clean` guard for canonical releases.
- **Tags: `:latest` plus an immutable `:<git-describe>`** per image; the AI image is built `FROM` the immutable base tag of the same run, never a racing `:latest`.
- **Credential: ephemeral.** `$SANDCASTLE_GHCR_TOKEN` is piped from the operator's env over `incus exec` stdin into `podman login --password-stdin`, then `podman logout` after the build — never persisted on the appliance, never on argv.
- **Consumption: no `--auto-update`.** OCI sources are not Incus aliases, and Sandcastle Images change only when an operator builds. `image build-remote` refreshes the host alias deterministically at the end of each run (`incus image copy ghcr:…:<ver> <remote>: --alias … --reuse`, then sync tenant aliases). GHCR remains distribution/archive for other hosts.

## Consequences

- The local `image:*:build-upload` (docker) tasks remain as the quick, offline, local-iteration path; `build-remote` (podman → GHCR) is the canonical, distributable path. One set of Dockerfiles, two engines.
- A one-time `ghcr` OCI remote (`incus remote add ghcr https://ghcr.io --protocol oci`) is required on the admin host; `image build-remote` ensures it idempotently.
- GHCR packages are public-read, so the Incus host needs no registry credentials to pull; the token is only for push.
- The persistent appliance keeps a warm podman layer cache on a dedicated Incus volume; `image builder destroy` removes the appliance and project (cache included unless `--keep-cache`).
