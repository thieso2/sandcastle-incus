# Context Map

This repo has two bounded contexts. Read this map, then the context(s) relevant
to what you're changing.

| Context | Glossary | ADRs | Scope |
| --- | --- | --- | --- |
| Sandcastle | [`CONTEXT.md`](CONTEXT.md) → [`docs/glossary.md`](docs/glossary.md) | [`docs/adr/`](docs/adr/) | The Go CLI, Auth App, brokers, and Incus integration. System-wide vocabulary. |
| sc-edge | [`sc-edge/CONTEXT.md`](sc-edge/CONTEXT.md) | [`sc-edge/docs/adr/`](sc-edge/docs/adr/) | The portable edge appliance (Caddy + `cloudflared`). |

The root `CONTEXT.md` is a pointer: the canonical term list lives in
[`docs/glossary.md`](docs/glossary.md), and the architecture overview in
[`docs/topology.md`](docs/topology.md).

`sc-edge` is a child context. Its glossary defines only edge-appliance terms and
defers to the root `CONTEXT.md` for Sandcastle-wide vocabulary. When working in
`sc-edge/`, read both.

Consumer rules for agents are in [`docs/agents/domain.md`](docs/agents/domain.md).
