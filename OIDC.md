# Sandcastle OIDC — Design and Usage

## Overview

Sandcastle Workload Identity lets machines authenticate to external cloud services (GCP, AWS, etc.) without storing long-lived credentials. The Auth App acts as an OIDC Provider. Machines exchange a per-machine runtime secret for short-lived RS256 JWTs, which are then used for cloud provider Workload Identity Federation.

```
Machine ──POST runtime-secret──► Auth App
         ◄── 15-min RS256 JWT ──
         ──JWT──► GCP / AWS STS
         ◄── short-lived cloud token ──
```

This is separate from GitHub OAuth login, which only authenticates humans into the Auth App.

---

## Legacy: Global OIDC

Older Auth App deployments operated a single global OIDC issuer shared across all tenants.

| Endpoint | URL |
|---|---|
| Discovery | `https://auth.host/.well-known/openid-configuration` |
| JWKS | `https://auth.host/.well-known/jwks.json` |
| Token | `https://auth.host/internal/workload/token` |

**Issuer:** `https://auth.host`

Machines on the legacy issuer receive an `iss` claim pointing to this shared issuer. Any cloud provider Workload Identity Pool configured to trust this issuer can accept tokens from any tenant's machine. This is acceptable in single-tenant deployments but prevents per-tenant cloud isolation.

---

## Per-Tenant OIDC

Each tenant gets its own OIDC issuer, its own signing key pair, and its own discovery/JWKS endpoints. Cloud provider trust relationships are scoped per tenant — a GCP pool that trusts tenant A cannot accept tokens minted for tenant B.

### Issuer URL Scheme

```
https://auth.host/t/{tenant}
```

Endpoints per tenant:

| Endpoint | URL |
|---|---|
| Discovery | `https://auth.host/t/{tenant}/.well-known/openid-configuration` |
| JWKS | `https://auth.host/t/{tenant}/.well-known/jwks.json` |
| Token | `https://auth.host/internal/workload/token` (shared — tenant/project/machine verified against runtime secret state) |

The token endpoint does not need to be per-tenant because the server verifies the requested tenant, project, and machine against the stored Machine Runtime Secret state before minting. The `iss` claim in the minted JWT uses the per-tenant issuer URL.

### Database Schema Change

Add a `tenant` column to `oidc_signing_keys`:

```sql
ALTER TABLE oidc_signing_keys ADD COLUMN tenant TEXT NOT NULL DEFAULT '';
```

Existing rows get `tenant = ''` — these remain valid as the "global" key set for backward compatibility. New machines get per-tenant keys.

Migration is safe: `ensureColumn("oidc_signing_keys", "tenant", "TEXT NOT NULL DEFAULT ''")` in `Migrate`.

### New HTTP Routes

```
GET /t/{tenant}/.well-known/openid-configuration
GET /t/{tenant}/.well-known/jwks.json
```

Global routes `/.well-known/openid-configuration` and `/.well-known/jwks.json` are kept for backward compatibility (serve keys where `tenant = ''`).

### Key Lifecycle

- `EnsureOIDCSigningKey(ctx, db, tenant)` — creates a key for `tenant` if none exists; called during `EnableMachineWorkloadIdentity` so the key is ready before the first token request.
- `activeOIDCPrivateKey(ctx, db, tenant)` — returns the decrypted RSA key for a specific tenant.
- `ListPublicOIDCSigningKeys(ctx, db, tenant)` — returns public JWKs for a specific tenant's JWKS endpoint.

### Files Changed by This Work

| File | Change |
|---|---|
| `internal/authapp/app.go` | `Migrate`: add `ensureColumn` for `tenant`; register 2 new per-tenant routes |
| `internal/authapp/oidc.go` | New `tenantOIDCIssuer(host, tenant)`; add `tenant` param to all key functions |
| `internal/authapp/workload.go` | `EnableMachineWorkloadIdentity`: ensure the tenant signing key; `mintWorkloadToken`: use per-tenant issuer + key + audience |
| `internal/machine/workload_identity.go` | Write executable-sourced GCP credential material |

### Migration for Existing Machines

Machines provisioned before per-tenant OIDC was deployed have no tenant helper files, or have `iss = https://auth.host` in older tokens. Re-running `sc workload enable --cloud-identity {name} [project:]machine` rotates their runtime secret, injects `/usr/local/bin/sandcastle-workload-token`, and moves them to the per-tenant issuer. Update the cloud provider trust configuration to match.

---

## Enabling Workload Identity on a Machine

```sh
sc workload enable [project:]machine --cloud-identity gcp
```

For a new machine, the same injection can happen during creation or connect auto-creation:

```sh
sc create [project:]machine --cloud-identity gcp
sc connect [project:]machine --cloud-identity gcp
```

This command:
1. Resolves the machine and its tenant from Incus state.
2. Uses the CLI Auth Token saved by `sc login` to authorize the current User with the Auth App.
3. Asks the Auth App to rotate the Machine Runtime Secret and store only its verifier.
4. Writes workload files into the machine through the user's tenant-scoped Incus remote.

### Files Written to the Machine

| Path | Mode | Content |
|---|---|---|
| `/var/lib/sandcastle/workload/runtime-secret` | `0600` | Per-machine secret used to request tokens |
| `/var/lib/sandcastle/workload/token-endpoint` | `0644` | Token endpoint URL |
| `/var/lib/sandcastle/workload/tenant` | `0644` | Tenant slug |
| `/var/lib/sandcastle/workload/project` | `0644` | Incus project name |
| `/var/lib/sandcastle/workload/machine` | `0644` | Machine name |
| `/usr/local/bin/sandcastle-workload-token` | `0755` | Helper executable that requests fresh Workload Identity Tokens |
| `/etc/profile.d/sandcastle-workload-identity.sh` | `0644` | Exports `SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE`, `SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE`, `SANDCASTLE_TENANT`, `SANDCASTLE_PROJECT`, `SANDCASTLE_MACHINE` |

If a GCP config is provided, additional files are written:

| Path | Mode | Content |
|---|---|---|
| `/var/lib/sandcastle/workload/gcp-audience` | `0644` | Cloud Identity Audience for the selected GCP provider |
| `/var/lib/sandcastle/workload/gcp-credential.json` | `0600` | GCP external account credential JSON using the helper executable |
| `/etc/profile.d/sandcastle-workload-identity.sh` | `0644` | Also exports `GOOGLE_EXTERNAL_ACCOUNT_ALLOW_EXECUTABLES`, `GOOGLE_APPLICATION_CREDENTIALS`, and `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE` |

---

## Getting a Workload Token (Machine Side)

```sh
TOKEN_ENDPOINT=$(cat /var/lib/sandcastle/workload/token-endpoint)
RUNTIME_SECRET=$(cat /var/lib/sandcastle/workload/runtime-secret)
TENANT=$(cat /var/lib/sandcastle/workload/tenant)
PROJECT=$(cat /var/lib/sandcastle/workload/project)
MACHINE=$(cat /var/lib/sandcastle/workload/machine)
AUDIENCE=$(cat /var/lib/sandcastle/workload/gcp-audience) # or set this to another Cloud Identity Audience

/usr/local/bin/sandcastle-workload-token token "$AUDIENCE"

curl -s -X POST "$TOKEN_ENDPOINT" \
  -H 'Content-Type: application/json' \
  -d "{\"tenant\":\"$TENANT\",\"project\":\"$PROJECT\",\"machine\":\"$MACHINE\",\"runtime_secret\":\"$RUNTIME_SECRET\",\"audience\":\"$AUDIENCE\"}"
```

Response:

```json
{
  "token_type": "Bearer",
  "access_token": "<RS256 JWT>",
  "expires_in": 900
}
```

### JWT Claims

| Claim | Example | Meaning |
|---|---|---|
| `iss` | `https://auth.host/t/acme` | Per-tenant issuer (or global in v1) |
| `sub` | `machine:acme/default/dev` | Tenant/project/machine |
| `aud` | `//iam.googleapis.com/projects/.../providers/sandcastle` | Cloud Identity Audience |
| `iat` / `nbf` / `exp` | Unix timestamps | Issued at / valid from / expires (15-minute window) |
| `tenant` | `acme` | Sandcastle tenant slug |
| `project` | `default` | Sandcastle project name |
| `machine` | `dev` | Machine name |
| `sandcastle_user_key` | `acme` | Auth DB user key associated with this machine |
| `github_username` | `acme` | GitHub username associated with this machine |

---

## GCP Workload Identity Federation

### One-Time GCP Setup (per tenant)

GCP Workload Identity Federation lets GCP workloads impersonate service accounts using external JWTs. After per-tenant OIDC is deployed, configure this per tenant.

The user CLI uses the Auth Hostname and CLI Auth Token remembered by `sc login`, plus the active `gcloud` project by default:

```sh
sc cloud-identity gcp setup \
  --tenant {tenant}
```

It creates or updates the Workload Identity Pool and OIDC provider, creates a service account if needed, grants `roles/iam.workloadIdentityUser`, and saves the user-owned Sandcastle Cloud Identity Config named `gcp` in the Auth App. The command also prints the GCP audience and service account impersonation URL so the web UI can be used to confirm or repair the saved config.

Verify the configured GCP and Sandcastle issuer state with:

```sh
scripts/verify-gcp-oidc.sh \
  --tenant {tenant} \
  --auth-hostname auth.host
```

#### 1. Create a Workload Identity Pool

```sh
gcloud iam workload-identity-pools create sandcastle-{tenant} \
  --project={gcp-project} \
  --location=global \
  --display-name="Sandcastle {tenant}"
```

#### 2. Create a Provider Pointing at the Tenant Issuer

```sh
gcloud iam workload-identity-pools providers create-oidc sandcastle \
  --project={gcp-project} \
  --location=global \
  --workload-identity-pool=sandcastle-{tenant} \
  --issuer-uri=https://auth.host/t/{tenant} \
  --allowed-audiences=//iam.googleapis.com/projects/{gcp-project-number}/locations/global/workloadIdentityPools/sandcastle-{tenant}/providers/sandcastle \
  --attribute-mapping="google.subject=assertion.sub,attribute.tenant=assertion.tenant,attribute.machine=assertion.machine"
```

> The `issuer-uri` must match the `iss` claim in the JWT exactly. GCP fetches `{issuer-uri}/.well-known/openid-configuration` to verify tokens.
> The token `aud` claim must match one of the provider's allowed audiences. The helper script sets it to the full provider resource name: `//iam.googleapis.com/projects/{gcp-project-number}/locations/global/workloadIdentityPools/sandcastle-{tenant}/providers/sandcastle`.

#### 3. Grant a Service Account to Be Impersonated

```sh
POOL="projects/{gcp-project-number}/locations/global/workloadIdentityPools/sandcastle-{tenant}"

gcloud iam service-accounts add-iam-policy-binding {sa}@{gcp-project}.iam.gserviceaccount.com \
  --project={gcp-project} \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://${POOL}/providers/sandcastle/attribute.machine/{machine-name}"
```

Scope the binding as tightly as needed: by `attribute.machine`, `attribute.tenant`, or `google.subject`.

### Compute the GCP Audience String

The audience is the full pool + provider resource name:

```
//iam.googleapis.com/projects/{gcp-project-number}/locations/global/workloadIdentityPools/sandcastle-{tenant}/providers/sandcastle
```

### Enable on a Machine with GCP Config

When a machine selects a GCP identity config, Sandcastle writes `gcp-credential.json` to the machine. The file format is GCP's external account credential:

```json
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/.../providers/sandcastle",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "credential_source": {
    "executable": {
      "command": "/usr/local/bin/sandcastle-workload-token gcp-executable",
      "timeout_millis": 30000
    }
  },
  "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa}@{project}.iam.gserviceaccount.com:generateAccessToken"
}
```

Google ADC runs the helper executable when it needs a subject token. The helper reads the Machine Runtime Secret and Cloud Identity Audience from machine-local files, requests a fresh Workload Identity Token from the Auth App, and returns executable-sourced credentials in the format expected by Google.

### Smoke Test (inside machine)

```sh
# Source workload identity exports
source /etc/profile.d/sandcastle-workload-identity.sh

# Verify the credential file is in place
cat $GOOGLE_APPLICATION_CREDENTIALS

# Try GCP authentication (requires gcloud or google-cloud-sdk)
gcloud auth application-default print-access-token
# or
python3 -c "import google.auth; creds, _ = google.auth.default(); creds.refresh(google.auth.transport.requests.Request()); print(creds.token)"
```

---

## Verifying the OIDC Provider

```sh
# Discovery (global — current)
curl https://auth.example.com/.well-known/openid-configuration

# Discovery (per-tenant — after migration)
curl https://auth.example.com/t/acme/.well-known/openid-configuration

# JWKS
curl https://auth.example.com/.well-known/jwks.json
curl https://auth.example.com/t/acme/.well-known/jwks.json
```

---

## Security Notes

- The raw runtime secret is returned once (at enable time) and never stored. Only its SHA-256 verifier is in the DB.
- OIDC private signing keys are stored AES-256-GCM encrypted in `oidc_signing_keys`. The encryption key is in `auth_app_meta` — both live in the same Auth Database, so database confidentiality is critical.
- JWKS endpoints publish public keys only.
- Token TTL is 15 minutes. Machines must re-request tokens before expiry.
- The Auth Hostname must be an HTTPS endpoint reachable from cloud provider STS for OIDC discovery fetches.
- Per-tenant isolation: once per-tenant OIDC is deployed, a GCP pool configured with one tenant's issuer cannot accept tokens from another tenant's machines because the JWKS sets are disjoint.
