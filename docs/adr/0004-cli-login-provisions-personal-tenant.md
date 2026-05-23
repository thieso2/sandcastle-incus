# CLI Login Provisions Personal Tenant

Sandcastle v1 provisions a user's Personal Tenant during `sandcastle login <auth-host>`, not when the user is allowlisted and not during browser-only login. This lets the CLI show provisioning progress while keeping allowlist edits lightweight and reversible.

## Considered Options

- Create a Personal Tenant immediately when a Sandcastle Admin adds a GitHub username to the Login Allowlist.
- Create a Personal Tenant on first browser-only GitHub OAuth Login.
- Create a Personal Tenant during first CLI Device Login.

## Consequences

- Browser-only GitHub OAuth Login creates a web session but no tenant infrastructure.
- CLI Device Login performs idempotent ensure-style provisioning for the User, Personal Tenant, Default Project, Tenant Access, and Incus credential enrollment.
- After Incus credential enrollment, CLI Device Login performs first-run local setup for a single accessible tenant: tenant DNS setup, local tenant CA trust installation, and tenant Tailscale sidecar activation. These steps must use the newly enrolled restricted Incus config, not the pre-login admin/default Incus config.
- CLI Device Login reports provisioning progress through polling status messages in v1.
- CLI Device Login returns an Incus Certificate Add Token so the CLI can generate and keep the private client key locally.
