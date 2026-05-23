# Secret-Bearing Infrastructure Seed Files

Sandcastle infrastructure creation uses a portable Infrastructure Seed File as the deployment bootstrap artifact instead of relying on environment-only setup. The seed is YAML, keyed by a Deployment Name, and may embed secrets and reusable working public TLS material so repeated `infra create` runs can recreate infrastructure without unnecessary Let's Encrypt issuance; command-line flags override environment variables, environment variables override seed values, and seed values override built-in defaults.

## Consequences

- `infra gen-seed` creates the editable seed at `~/.config/sandcastle/<deployment-name>.seed.yml` by default; `infra create` may create that seed when it is missing.
- `infra create` reads the seed, restores embedded Caddy ACME data before Caddy starts, and in ACME mode automatically writes back captured reusable TLS material after successful provisioning.
- `infra create` must not write transient CLI or environment overrides back to the seed.
- Embedded public TLS material records the Auth Hostname it belongs to, and `infra create` fails rather than restoring it for a different Auth Hostname.
- `infra create` prepares the configured Sandcastle base and AI images before launch; full `oci:` image references opt out of local build/upload.
- Seed files must be written with private file permissions and treated as operator secrets.
