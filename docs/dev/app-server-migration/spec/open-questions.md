# App Server Migration: Open Questions

This file no longer mixes planning blockers with harmless payload-shape details.

If an item appears under `Blockers Before Implementation`, the migration plan should not pretend it is already solved.

## Blockers Before Implementation

### Data Adoption Strategy

- Exact lazy migration or adoption mechanics for existing persisted session data.
- Minimum metadata additions required for project registry, run metadata, approval state, and process metadata without forcing destructive storage rewrite.

### Boundary Enforcement

- Exact CI or static-check mechanism that will enforce the frontend or server import boundary once the refactor starts.
- Whether the repo wants a dedicated import-boundary test, linter rule, or build-tag based enforcement.

### Black-Box Proof Surface

- The exact acceptance-test harness that proves the CLI now talks through the client boundary instead of privileged runtime access.
- The minimum reference non-CLI test client scope needed to prove the protocol is real.

### Local Discovery And Startup UX

- Exact local discovery mechanism for the well-known local control endpoint or socket across supported operating systems.
- Exact attach-or-start CLI UX when a compatible or incompatible local server is already present.

## Later Schema Questions

### Method And Payload Shape

- Exact JSON-RPC method names inside the locked resource-oriented taxonomy.
- Exact handshake request and response JSON shape after incompatible versions are rejected explicitly.
- Exact `attach` acknowledgment payload shape for minimal attached-resource metadata.
- Exact structured submission envelope shape, including namespacing for future `client_meta` fields.

### Read Models And Streams

- Exact payload schemas for typed hydration views such as `session.getMainView`.
- Exact normalized event payload shapes for durable state streams.
- Exact payload shapes for live session activity and process output streams.
- Exact cursor and retention metadata that the protocol should expose.

## Closed Questions

These are intentionally no longer open:

- Compatibility slash-command invocation does not need a dedicated protocol affordance. Slash syntax remains frontend-only.
- Richer frontend-owned command metadata may eventually cross the boundary through a structured submission envelope, not through server-provisioned slash commands.
- Replay is not the normal reconnect path. Rehydrate first, then resubscribe, with best-effort catch-up only when available.
- Runtime tuning operations such as `/thinking` and `/fast` are session-scoped live settings rather than per-run-only settings.
