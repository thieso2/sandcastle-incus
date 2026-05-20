# Domain Docs

How the engineering skills should consume this repo's domain documentation when
exploring the codebase.

## Before Exploring, Read These

- `CONTEXT.md` at the repo root, if it exists.
- `docs/adr/`, if it exists, for ADRs that touch the area being changed.

If these files do not exist, proceed silently. Do not flag their absence or
suggest creating them upfront. Producer skills create them lazily when terms or
decisions are resolved.

## File Structure

This repo uses a single-context layout:

```text
/
├── CONTEXT.md
├── docs/adr/
└── docs/
```

## Use The Glossary's Vocabulary

When output names a domain concept, use the term as defined in `CONTEXT.md`.
If the concept is missing, note the gap only when it matters to the task.

## Flag ADR Conflicts

If output contradicts an existing ADR, surface it explicitly rather than
silently overriding it.

