# GitHub Username Identity for Personal Tenants

Sandcastle v1 uses the normalized GitHub username as the Sandcastle User Key, Personal Tenant name, and Personal Tenant DNS suffix. This accepts GitHub rename risk in exchange for friend-readable tenant names and hostnames such as `codex.default.octocat`.

## Considered Options

- Use GitHub's numeric account ID for stable identity and tenant names.
- Use the normalized GitHub username everywhere in Sandcastle v1.

## Consequences

- Personal Tenants use GitHub-compatible username validation, including usernames that start with digits.
- Non-personal admin-created Tenants keep the existing stricter Sandcastle tenant naming rule.
- The Login Allowlist is managed by GitHub username and authorizes by normalized GitHub username.
- The Auth App still stores GitHub numeric account ID as audit and future migration metadata.
- A GitHub account rename blocks login until a Sandcastle Admin performs migration or allowlist repair.
