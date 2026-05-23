# Sandcastle Login Process

This document describes the end-to-end first-run login process for a GitHub-authenticated user who wants to create and connect to a container-backed Machine.

## Goals

- Let a user register in the browser with GitHub.
- Keep browser registration separate from tenant provisioning.
- Use the CLI to provision the user's Personal Tenant and upload the user's SSH public key.
- Prefer Tailscale Machine IPs for Machine SSH Access when they are available, while still allowing private-IP SSH in environments where the tenant private network is directly reachable.
- Finish login only when the user can create and connect to a Machine.

## Flow

1. The user opens the Auth App and completes GitHub OAuth Login.
2. Web Registration creates or confirms the Sandcastle User session.
3. The Onboarding Page shows the GitHub identity, allowlist status, CLI install instructions, and the exact login command.
4. The user installs the CLI and runs:

   ```sh
   sandcastle login https://<auth-host>
   ```

5. CLI Device Login opens the browser authorization flow.
6. After authorization, the browser tells the user to return to the terminal.
7. The CLI prepares the SSH key:
   - use `--ssh-public-key <path>` when supplied,
   - otherwise use `~/.ssh/id_ed25519.pub` when it exists,
   - otherwise create a local Sandcastle SSH Key at `~/.ssh/sandcastle_ed25519`.
8. The Auth App performs idempotent provisioning for the selected tenant:
   - ensure User identity,
   - ensure Personal Tenant for first-time onboarding,
   - ensure Default Project,
   - ensure Tenant Infrastructure,
   - ensure the Tenant Tailnet,
   - enroll the local Incus client certificate,
   - store the current User SSH Public Key,
   - reconcile that SSH key onto existing Machines in the User's Personal Tenant,
   - write that SSH key to Personal Tenant metadata so future Machines receive it during bootstrap.
9. The CLI guides the user through joining the selected Tenant Tailnet and verifies the local Tailscale client is connected to that tailnet.
10. CLI Device Login reaches Login Readiness only when credentials, SSH access, Personal Tenant, Default Project, Tenant Infrastructure, Tenant Tailnet access, and local CLI configuration are ready.
11. The CLI stores local configuration and prints:

    ```sh
    sandcastle create dev
    ```

12. The user creates the first Machine:

    ```sh
    sandcastle create dev
    ```

13. Machine creation records the Machine's Tailscale IP when one is available.
14. The CLI connects with Machine SSH Access over the recorded Tailscale Machine IP, or over the private Machine IP when no Tailscale Machine IP is recorded.

## Browser Responsibilities

The browser handles GitHub OAuth Login, Web Registration, allowlist feedback, and CLI onboarding instructions. It does not upload SSH keys, provision tenant infrastructure, or show authoritative provisioning progress.

The browser page after CLI authorization only confirms authorization and sends the user back to the terminal.

## CLI Responsibilities

The CLI owns the login provisioning experience. It prints terminal progress for tenant creation, infrastructure readiness, SSH key upload, Tenant Tailnet join, and Login Readiness.

Provisioning is idempotent. If login fails after creating some resources, a later `sandcastle login https://<auth-host>` resumes from the current state instead of rolling resources back.

## SSH Key Policy

Sandcastle stores one current User SSH Public Key per User in v1. Each successful CLI Device Login replaces the stored public key when the uploaded key differs.

The matching private key stays local to the user's machine. Sandcastle never asks the user to paste a private SSH key into the browser.

When Tenant Access is revoked, Sandcastle revokes Machine SSH Access by removing that User's SSH public key from Machines in that Tenant.

## Tailscale Policy

Each Tenant has exactly one Tenant Tailnet. Sandcastle does not use a shared Sandcastle tailnet for all tenants.

Machine SSH Access uses the Machine's Tailscale Machine IP when recorded, otherwise it falls back to the private Machine IP. Local DNS is useful for private hostnames, but it is not required for Machine SSH Access.

For multi-tenant users, CLI Device Login joins only the selected Current Tenant's Tenant Tailnet. First-time onboarding defaults the selected tenant to the user's Personal Tenant.

## Login Result

CLI Device Login ends with a structured CLI Login Result that includes the selected User, Current Tenant, Current Project, credential enrollment data, SSH key fingerprint, Tenant Tailnet status, and next command.
