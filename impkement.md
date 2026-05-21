# Sandcastle Auth App Implementation Goal

Goal: Implement the Sandcastle Auth App plan end-to-end from GitHub issues #8 through #17.

## Before Coding

- Read `CONTEXT.md`.
- Read `docs/adr/0002-go-auth-app-with-sqlite.md`.
- Read `docs/adr/0003-github-username-identity-for-personal-tenants.md`.
- Read `docs/adr/0004-cli-login-provisions-personal-tenant.md`.
- Read `docs/adr/0005-sandcastle-oidc-provider-for-machine-workload-identity.md`.
- Read GitHub issues #8, #9, #10, #11, #12, #13, #14, #15, #16, and #17.
- Inspect existing infrastructure, Caddy, route broker, usertrust, tenant, machine, config, CLI, and e2e test patterns.

## Environment

- A local Incus installation is available.
- You may create disposable Incus VMs/containers for testing.
- Real e2e tests are allowed when useful.
- A Tailscale auth key will be provided in the repo `.env` file.
- Treat all real Incus resources as disposable only when clearly named/scoped for tests.
- Do not delete or mutate non-test Incus resources.

## Implementation Order

1. #8 Deploy Minimal Auth App Infrastructure
2. #9 Implement GitHub-Only Login and Admin Bootstrap
3. #10 Build Login Allowlist Management
4. #11 Add CLI Device Login Approval Flow
5. #12 Provision Personal Tenant During CLI Login
6. #13 Return Incus Certificate Add Token from CLI Login
7. #14 Add Auth App Admin Tenant Access UI
8. #15 Implement Sandcastle OIDC Provider Endpoints
9. #16 Inject Machine Runtime Secret and Mint Workload Identity Tokens
10. #17 Add User Cloud Identity Configs and GCP Credential Injection

## Scope Rules

- Implement each issue as a vertical slice and keep the acceptance criteria traceable.
- Respect the domain language in `CONTEXT.md`: Tenant, Personal Tenant, User, Login Allowlist, Auth App, CLI Device Login, Workload Identity Token, Machine.
- Do not introduce password login.
- GitHub is the only external login provider in v1.
- Use Normalized GitHub Username as the v1 Sandcastle User Key and Personal Tenant name.
- Store GitHub numeric account ID only as metadata for audit/future migration.
- Store GitHub Email as inactive metadata only; do not use it for identity, notifications, allowlisting, tenant names, or OIDC subject claims.
- Personal Tenant provisioning happens during `sandcastle login <auth-host>`, not at allowlist add time or browser-only login time.
- CLI login must return an Incus Certificate Add Token, never a generated client private key.
- Auth App uses Go, minimal server-rendered HTML, SQLite, and a separate infrastructure service from the Route Broker.
- Auth Hostname is reserved infrastructure routing, not a user Public Route.
- OIDC claims must use Tenant/Project/Machine vocabulary, not legacy `sandbox`.

## Engineering Expectations

- Follow existing Go patterns in the repo.
- Reuse existing Incus tenant, usertrust, route broker, Caddy, config, and CLI logic where appropriate.
- Add focused unit tests and e2e tests where the repo already has patterns.
- Keep unrelated refactors out.
- Update docs when commands, env vars, workflows, or user-visible behavior change.
- Preserve existing CLI behavior unless the issue explicitly changes it.
- Preserve existing public route and Route Broker behavior.
- Make e2e resources clearly disposable and clean them up.

## Verification

- Run relevant unit tests after each major slice.
- Run broader `go test ./...` when feasible.
- Use local Incus for real integration/e2e validation where it materially reduces risk.
- Use the Tailscale auth key from `.env` for Tailscale-related e2e paths if needed.
- Clearly report any tests that cannot run and why.

## Delivery

- Prefer small commits or clear checkpoints per issue.
- At the end, summarize each issue #8-#17 with implemented status, key files changed, tests run, e2e coverage, and remaining gaps.
