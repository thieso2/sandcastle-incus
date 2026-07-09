# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT-MAP.md`** at the repo root — this repo is multi-context. The map names each context and points at its glossary and ADRs. Read it first, then read the context(s) relevant to what you're changing.
- **`docs/adr/`** — system-wide ADRs. Read the ones that touch the area you're about to work in.
- **The context-scoped `docs/adr/`** for whichever context you're in (e.g. `sc-edge/docs/adr/`).

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily when terms or decisions actually get resolved.

## File structure

This repo uses a multi-context layout. Contexts are top-level directories, not `src/<context>/`:

```text
/
├── CONTEXT-MAP.md      ← index of contexts; read first
├── CONTEXT.md          ← Sandcastle-wide vocabulary (root context)
├── docs/
│   ├── glossary.md     ← the actual canonical term list (see hop below)
│   ├── topology.md     ← architecture overview
│   └── adr/            ← system-wide decisions
└── sc-edge/
    ├── CONTEXT.md      ← edge-appliance glossary
    └── docs/adr/       ← context-scoped decisions
```

## The root glossary is one hop further

The root `CONTEXT.md` is a **pointer**, not the term list. It defers to
[`docs/glossary.md`](../glossary.md) for canonical vocabulary and
[`docs/topology.md`](../topology.md) for the architecture overview. When a skill
is told to "read `CONTEXT.md`", follow that hop — the terms you need are in
`docs/glossary.md`.

## `sc-edge` is a child context

`sc-edge/CONTEXT.md` defines only edge-appliance terms (Caddy, `cloudflared`, the
three ingress modes) and explicitly defers to the root `CONTEXT.md` for
Sandcastle-wide vocabulary. When working in `sc-edge/`, read **both** the child
context and the root — not just the child. `sc-edge/` also carries its own
`CLAUDE.md`.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in the glossary for the context you're in. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal — either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0013 (public routes stay shared infrastructure) — but worth reopening because…_

Check both `docs/adr/` and the context-scoped ADR directory for the context you're working in.
