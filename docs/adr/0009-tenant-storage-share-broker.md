# Tenant Storage Shares use a privileged broker

Tenant Storage Shares cross the normal tenant Incus project boundary, so Sandcastle will not let restricted user credentials directly attach another tenant's storage into a recipient tenant. Share grant, acceptance, and revocation requests are authorized against Tenant Access, then applied by a narrow privileged broker that mutates share state and reconciles read-only machine mounts. This keeps the user-facing permission model tenant-based while preserving the Incus project boundary for normal CLI credentials.

