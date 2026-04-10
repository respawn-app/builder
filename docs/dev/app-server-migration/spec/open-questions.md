# App Server Migration: Open Questions

This file no longer mixes planning blockers with harmless payload-shape details.

If an item appears under `Blockers Before Implementation`, the migration plan should not pretend it is already solved.

## Blockers Before Implementation

### Data Adoption Strategy

- Exact cutover verification and recovery procedure if the final staged migration step fails after the old tree has already been moved into backup space.
- Exact legacy-to-new mapping for edge cases such as malformed `session.json`, missing `events.jsonl`, or partially durable lazy sessions discovered during migration.

### Boundary Enforcement

- Exact CI or static-check mechanism that will enforce the frontend or server import boundary once the refactor starts.
- Whether the repo wants a dedicated import-boundary test, linter rule, or build-tag based enforcement.

### Black-Box Proof Surface

- The exact acceptance-test harness that proves the CLI now talks through the client boundary instead of privileged runtime access.
- The minimum reference non-CLI test client scope needed to prove the protocol is real.

### Local Discovery And Startup UX

- Exact app-global local discovery mechanism for the well-known local control endpoint or socket across supported operating systems once workspace-scoped discovery is removed.
- Exact attach-or-start CLI UX when a compatible or incompatible local server is already present.
- Exact startup flow when the user's current cwd resolves to a known project but unknown worktree.

### Phase 4 Project Scope

- Exact availability aggregation rules from workspace/worktree state to project state.

### Hybrid Storage Details

- Exact SQLite table boundaries for session metadata versus auxiliary JSON columns.
- Exact repair rules for SQLite summary drift after transcript-file append succeeds but metadata transaction fails.

### Runtime Lease Contract

- Exact disconnect cleanup policy and timeout model for abandoned leases.

## Later Schema Questions

### Method And Payload Shape

- Exact JSON-RPC method names inside the locked resource-oriented taxonomy.
- Exact handshake request and response JSON shape after incompatible versions are rejected explicitly.
- Exact `attach` acknowledgment payload shape for minimal attached-resource metadata.
- Exact structured submission envelope shape, including namespacing for future `client_meta` fields.

### Read Models And Streams

- Exact payload schemas for typed hydration views such as `session.getMainView`.
- Exact normalized event payload shapes for durable state streams.
- Exact payload shapes for live session activity, prompt activity, and process output streams.
- Exact transcript paging and compression policy for large sessions.

## Closed Questions

These are intentionally no longer open:

- Compatibility slash-command invocation does not need a dedicated protocol affordance. Slash syntax remains frontend-only.
- Richer frontend-owned command metadata may eventually cross the boundary through a structured submission envelope, not through server-provisioned slash commands.
- Reconnect uses authoritative hydration views and transcript pages, then fresh subscriptions. A stream-history or cursor recovery contract is not required.
- Frontend state management must converge on one authoritative committed-transcript cache plus separate ephemeral live state; projection and render caches are derived-only.
- Runtime tuning operations such as `/thinking` and `/fast` are session-scoped live settings rather than per-run-only settings.
- Current ask/approval restart behavior is transcript-driven rather than broker-queue-driven: interrupted tool-call attempts remain in conversation state, reopen appends the interruption marker, and the next model turn re-evaluates what to do.
- Pending asks and approvals are delivered live through a dedicated prompt activity stream; `ask.listPendingBySession` and `approval.listPendingBySession` remain hydration reads rather than the primary live-delivery path.

Resolved storage and migration policy now lives in:

- `spec/persistence-model.md`
- `spec/locked-decisions.md`
- `docs/dev/decisions.md`
