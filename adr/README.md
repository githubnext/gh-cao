# Architecture decision records

ADRs in this directory are durable decision history, not temporary implementation
notes. Keep an ADR when it records a significant architectural, security,
authority, protocol, data-shape, or product-architecture choice whose rationale
and tradeoffs remain useful after implementation.

Normative requirements belong in `specs/` or the relevant specification-style
documentation. When an ADR's only remaining value is a requirement, field list,
view contract, or implementation summary that is already captured there, remove
the ADR rather than keeping a duplicate source of truth. If a later decision
replaces an ADR but the historical rationale still matters, mark it superseded
or add a replacement ADR; if the record is stale and no longer describes an
active decision, delete it.

Use this root `adr/` directory for ADRs. Do not create a parallel `docs/adr/`
tree.

## Campaign knowledge proposal

[Lazy, storage-independent campaign knowledge services](campaign-knowledge-service.md)
records the proposed campaign data/graph, activation, embedding, and query
boundaries. Its [supporting research](research/campaign-knowledge-service.md)
preserves the source survey and illustrative design sketches; neither document
changes current normative contracts or activates a service.
