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

## Current State: Global OIDC

The Auth App currently operates a single global OIDC issuer shared across all tenants.

| Endpoint | URL |
|---|---|
| Discovery | `https://auth.host/.well-known/openid-configuration` |
| JWKS | `https://auth.host/.well-known/jwks.json` |
| Token | `https://auth.host/internal/workload/token` |

**Issuer:** `https://auth.host`

All machines across all tenants receive a `iss` claim pointing to this shared issuer. Any cloud provider Workload Identity Pool configured to trust this issuer would accept tokens from any tenant's machine. This is acceptable in single-tenant deployments but prevents per-tenant cloud isolation.

---

## Planned: Per-Tenant OIDC

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
| Token | `https://auth.host/internal/workload/token` (shared — tenant resolved from runtime secret) |

The token endpoint does not need to be per-tenant because the server looks up the tenant from the runtime secret at request time. The `iss` claim in the minted JWT will use the per-tenant issuer URL regardless.

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
| `internal/authapp/workload.go` | `mintWorkloadToken`: use per-tenant issuer + key |
| `internal/authapp/cloud_identity.go` | `EnableMachineWorkloadIdentity`: call `EnsureOIDCSigningKey` with tenant |

### Migration for Existing Machines

Machines provisioned before per-tenant OIDC was deployed have `iss = https://auth.host` in their tokens. If those machines had cloud federation configured with the global issuer, re-running `sc-adm workload enable` will rotate their runtime secret and move them to the per-tenant issuer. Update the cloud provider trust configuration to match.

---

## Enabling Workload Identity on a Machine

```sh
sc-adm workload enable [project:]machine \
  --database /var/lib/sandcastle/auth/auth.db \
  --auth-hostname auth.example.com
```

This command:
1. Resolves the machine and its tenant from Incus state.
2. Calls `EnableMachineWorkloadIdentity` in the auth DB: stores a new runtime secret verifier, returns the plaintext secret once.
3. Writes workload files into the machine via Incus file push.

### Files Written to the Machine

| Path | Mode | Content |
|---|---|---|
| `/var/lib/sandcastle/workload/runtime-secret` | `0600` | Per-machine secret used to request tokens |
| `/var/lib/sandcastle/workload/token-endpoint` | `0644` | Token endpoint URL |
| `/var/lib/sandcastle/workload/tenant` | `0644` | Tenant slug |
| `/var/lib/sandcastle/workload/project` | `0644` | Incus project name |
| `/var/lib/sandcastle/workload/machine` | `0644` | Machine name |
| `/etc/profile.d/sandcastle-workload-identity.sh` | `0644` | Exports `SANDCASTLE_WORKLOAD_RUNTIME_SECRET_FILE`, `SANDCASTLE_WORKLOAD_TOKEN_ENDPOINT_FILE`, `SANDCASTLE_TENANT`, `SANDCASTLE_PROJECT`, `SANDCASTLE_MACHINE` |

If a GCP config is provided, two additional files are written:

| Path | Mode | Content |
|---|---|---|
| `/var/lib/sandcastle/workload/gcp-credential.json` | `0644` | GCP external account credential JSON |
| `/etc/profile.d/sandcastle-workload-identity.sh` | `0644` | Also exports `GOOGLE_APPLICATION_CREDENTIALS` and `CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE` |

---

## Getting a Workload Token (Machine Side)

```sh
TOKEN_ENDPOINT=$(cat /var/lib/sandcastle/workload/token-endpoint)
RUNTIME_SECRET=$(cat /var/lib/sandcastle/workload/runtime-secret)
TENANT=$(cat /var/lib/sandcastle/workload/tenant)
PROJECT=$(cat /var/lib/sandcastle/workload/project)
MACHINE=$(cat /var/lib/sandcastle/workload/machine)

curl -s -X POST "$TOKEN_ENDPOINT" \
  -H 'Content-Type: application/json' \
  -d "{\"tenant\":\"$TENANT\",\"project\":\"$PROJECT\",\"machine\":\"$MACHINE\",\"runtime_secret\":\"$RUNTIME_SECRET\"}"
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
| `sub` | `machine:acme/sc-acme-default/dev` | Tenant/project/machine |
| `iat` / `exp` | Unix timestamps | Issued at / expires (15-minute window) |
| `tenant` | `acme` | Sandcastle tenant slug |
| `project` | `sc-acme-default` | Incus project name |
| `machine` | `dev` | Machine name |
| `sandcastle_user_key` | `acme` | Auth DB user key associated with this machine |
| `github_username` | `acme` | GitHub username associated with this machine |

---

## GCP Workload Identity Federation

### One-Time GCP Setup (per tenant)

GCP Workload Identity Federation lets GCP workloads impersonate service accounts using external JWTs. After per-tenant OIDC is deployed, configure this per tenant.

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
  --allowed-audiences=https://auth.host/t/{tenant} \
  --attribute-mapping="google.subject=assertion.sub,attribute.tenant=assertion.tenant,attribute.machine=assertion.machine"
```

> The `issuer-uri` must match the `iss` claim in the JWT exactly. GCP fetches `{issuer-uri}/.well-known/openid-configuration` to verify tokens.

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

When per-tenant OIDC and a GCP identity config are wired together via `sc-adm workload enable`, Sandcastle writes `gcp-credential.json` to the machine. The file format is GCP's external account credential:

```json
{
  "type": "external_account",
  "audience": "//iam.googleapis.com/projects/.../providers/sandcastle",
  "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_url": "https://sts.googleapis.com/v1/token",
  "credential_source": {
    "file": "/var/lib/sandcastle/workload/gcp-credential.json",
    "format": { "type": "text" }
  },
  "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa}@{project}.iam.gserviceaccount.com:generateAccessToken"
}
```

Wait — there's a self-reference problem here: `credential_source.file` points to the credential file itself. The actual flow is:

1. Sandcastle writes the JWT (obtained from `/internal/workload/token`) to a token file.
2. `gcp-credential.json` points `credential_source.file` at that token file.
3. Google ADC reads the token file, exchanges it at `sts.googleapis.com`, and returns a GCP access token.

In practice, a sidecar or helper script fetches the Sandcastle JWT first and writes it to the token file path referenced in `gcp-credential.json`. The credential file itself does not self-reference.

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
