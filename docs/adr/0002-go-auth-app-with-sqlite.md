# Go Auth App with SQLite

Sandcastle will add an Auth App as a Go service in the existing codebase, using a single-instance SQLite database on persistent infrastructure storage. This keeps Incus integration, tenant access rules, Caddy rendering, and CLI protocol code in one runtime while avoiding Rails/Postgres operational weight for a small control-plane service.

## Considered Options

- A small Rails app using Devise and a relational database.
- A Go service using SQLite and server-rendered HTML.
- Store all auth state in Incus metadata.

## Consequences

- The Auth App runs as its own infrastructure service, separate from the Route Broker.
- SQLite is sufficient for v1 because the Auth App is single-instance and low-write.
- Moving to horizontal Auth App scaling later requires moving the Auth Database to a multi-writer database such as Postgres.
- Incus metadata remains the source of truth for tenant runtime state, not browser sessions, device codes, signing keys, or login records.
