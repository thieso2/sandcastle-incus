# Sandcastle OIDC Provider for Machine Workload Identity

Sandcastle will act as an OIDC Provider for Machine workload identity, issuing short-lived tokens that identify both the User and the Machine using Tenant, Project, and Machine vocabulary. This carries forward the proven cloud federation pattern from the earlier Rails implementation while avoiding the legacy `sandbox` terminology.

## Considered Options

- Inject long-lived cloud credentials into Machines.
- Depend on workstation credential forwarding.
- Mint short-lived OIDC Workload Identity Tokens from Sandcastle.

## Consequences

- The Auth Hostname is the stable public issuer host and is reserved infrastructure routing, not a user Public Route.
- Workload Identity Tokens expire after 15 minutes in v1.
- Machines authenticate token requests with per-Machine runtime secrets; the Auth App stores only verifiers.
- OIDC Signing Keys are stored encrypted in the Auth Database and public keys are exposed via JWKS.
- Cloud Identity Configs are user-owned and selected per Machine in v1.
