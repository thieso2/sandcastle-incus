# First-Run Login Implementation Prompt

You are working in `/home/thies/sandcastle-incus`.

Goal: implement the full first-run GitHub CLI login feature from parent PRD
#18, issue by issue, committing and testing each step. The completed work should
fully satisfy every requirement in PRD #18. Close each child issue as it is
completed. Build E2E coverage, including a mock GitHub OAuth system so the full
login flow can be exercised locally without real GitHub OAuth.

## Project Instructions

- Read `AGENTS.md` and `CLAUDE.md` first.
- Use the domain vocabulary from `CONTEXT.md`.
- Respect the ADRs in `docs/adr/`.
- Do not modify unrelated code.
- Use existing repo patterns and tests.
- Update docs whenever CLI commands, flags, workflows, or setup steps change.
- Run focused tests after each issue and broader tests before closing the issue.
- Commit after each issue is complete, using a message that references the issue
  number.
- Close each issue only after tests pass and the committed implementation
  satisfies acceptance criteria.

## Issues To Implement In Order

1. #19 Onboarding Page shows CLI login path
2. #20 CLI login prepares User SSH Public Key
3. #21 Device Login accepts SSH key and returns CLI Login Result
4. #22 CLI Device Login idempotently ensures Personal Tenant readiness
5. #23 Login reconciles User SSH Public Key onto Personal Tenant Machines
6. #24 CLI Login verifies selected Tenant Tailnet readiness
7. #25 Machine create waits for Tailscale Machine IP
8. #26 Machine connect uses Tailscale Machine IP
9. #27 Tenant Access revocation removes Machine SSH Access
10. #28 First-run login E2E coverage and docs

## Workflow For Each Issue

1. Fetch the issue:

   ```sh
   gh issue view <number> --comments
   ```

2. Inspect the relevant code and tests.
3. Implement the smallest complete vertical slice satisfying the issue.
4. Add or update tests for externally observable behavior.
5. Run focused tests for the touched packages.
6. Run broader tests when the issue touches shared login, auth, Incus,
   Tailscale, or Machine behavior.
7. Update docs if CLI behavior, flags, setup, or workflow changed.
8. Commit the change:

   ```sh
   git add ...
   git commit -m "<short summary> (#<issue>)"
   ```

9. Close the issue with a short verification note:

   ```sh
   gh issue close <number> --comment "Implemented in <commit>. Tests: <commands run>."
   ```

## Testing Expectations

- Prefer unit and integration tests that assert public behavior, API contracts,
  CLI output, persisted state, and retry behavior.
- Avoid testing private helper structure unless it is the only practical seam.
- Keep tests deterministic.
- Add E2E coverage for the full first-run path.

## Mock GitHub OAuth Requirement

Create a mock GitHub OAuth system for local and CI integration tests. It should
simulate:

- GitHub OAuth authorization redirect
- OAuth token exchange
- GitHub user profile lookup
- allowlisted and non-allowlisted users
- stable GitHub account ID, login, and email
- denied or invalid OAuth states where useful

The Auth App should be configurable to use the mock GitHub OAuth provider in
tests without changing production defaults.

## Local Incus VM Integration Requirement

Use the local Incus setup to create a VM or test environment for integration and
E2E tests. Prefer existing scripts and harnesses in:

- `scripts/`
- `internal/e2e/`
- `mise.toml`
- `Makefile`

Before inventing new setup, inspect existing E2E patterns. The E2E environment
should be able to run:

- Auth App with mock GitHub OAuth
- CLI Device Login against the Auth App
- Personal Tenant provisioning
- SSH key upload
- Tenant Tailnet readiness using local or testable Tailscale behavior, or a
  deterministic mock where real Tailscale is unavailable
- `sandcastle create dev`
- connect target planning using the recorded Tailscale Machine IP

## Important Implementation Constraints

- Browser Web Registration must not provision tenant infrastructure.
- The CLI owns Login Readiness.
- The CLI must never upload or expose private SSH keys.
- CLI Device Login must be idempotent and retryable after partial failure.
- First-time onboarding defaults to the User's Personal Tenant.
- Multi-tenant login must not silently choose a tenant when ambiguous.
- CLI Device Login joins and verifies only the selected Current Tenant's Tenant
  Tailnet.
- Machine SSH Access must use the recorded Tailscale Machine IP directly.
- Local DNS can remain useful, but it must not be required for
  `sandcastle connect`.
- Revoking Tenant Access must remove Machine SSH Access without deleting tenant
  resources.

## Final Verification Before Completing The Parent Feature

- All child issues #19-#28 are closed.
- PRD #18 is fully satisfied by the implemented behavior, tests, E2E coverage,
  and docs.
- Parent #18 remains open unless explicitly instructed otherwise.
- Run the complete test suite practical for this repo.
- Run the new E2E flow using the local Incus VM/test setup.
- Provide a final summary with:
  - issue numbers closed
  - commits created
  - tests run
  - E2E command used
  - any residual manual setup needed
  - any known limitations
