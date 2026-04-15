# Completed App Server Migration Work

This file archives phases that are complete so `plan.md` can stay focused on open work.

## Completed Phases

### Phase 0: Freeze Behavior And Define Proof Obligations

Completed.

Outcome:

- behavior-preservation baseline defined
- persistence and workflow audit completed
- proof obligations and characterization groundwork established

### Phase 1: Create A Transport-Neutral Server API In Process

Completed.

Outcome:

- frontend/server boundary established behind transport-neutral client APIs
- loopback/in-process client path landed
- CLI/TUI no longer depend on direct runtime ownership as the main architectural path

### Phase 3: Add Real Transport And Local Daemon Mode

Completed.

Outcome:

- JSON-RPC-over-WebSocket gateway landed
- handshake and attach lifecycle landed
- `builder serve` landed
- CLI attach-or-start over real transport landed

### Phase 4: Storage Model Lock And Metadata Foundation

Completed.

Outcome:

- hybrid persistence model locked
- SQLite metadata authority landed
- one-time session metadata cutover landed
- runtime lease and execution-target cutover landed
- app-global direct attach topology landed
- explicit workspace binding UX and headless recovery commands landed

Subphases completed:

- Phase 4A: SQLite metadata introduction
- Phase 4B: staged storage migration and session metadata cutover
- Phase 4C: execution target and lease cutover
- Phase 4D: app-global direct attach and multi-project daemon cutover
- Phase 4E: workspace binding UX and headless recovery

### Phase 5A: Transcript Authority, Recovery, And Failure Semantics

Completed.

Outcome:

- committed transcript authority model landed
- ongoing/detail/reconnect flows now hydrate from the same committed transcript source
- explicit commit notifications and revision-aware tail hydration landed
- transport failures and stream gaps now recover through authoritative transcript state

### Phase 5B: Realtime Multi-Client Proof

Completed.

Outcome:

- multi-client realtime tests landed
- reconnect, ask, approval, and lifecycle race coverage landed
- remote and loopback active-session paths now share the same correctness model

### Phase 6A: Align Transcript Semantics With Product Contract

Completed.

Outcome:

- compaction modeled as same-session committed progression
- rollback/fork modeled as navigation to a different session target
- TUI ongoing-buffer reissue restricted to external continuity-loss cases only

### Phase 7: Standalone Polish And Boundary Proof

Completed.

Outcome:

- rollback selection targeting/highlighting is fixed and covered
- release-blocking startup and attach failure states are covered by tests
- external-daemon acceptance proof is green
- frontend caches/projection state are explicitly treated as non-durable transcript views
- direct-address attach and fail-fast workspace binding docs are reconciled

## Still Partially Open Elsewhere

These historical phases still have residual open work tracked in `plan.md`:

- Phase 2: resource surfaces and event hub
- Phase 6B: transcript divergence hardening
- Phase 8: shared frontend transcript architecture

## Completed Phase 6B Slices

These landed inside the still-open Phase 6B and are archived here so the active plan can stay focused on unresolved work.

- runtime event intake no longer coalesces `assistant_delta` / `reasoning_delta` before UI application
- runtime-committed assistant/tool/reviewer rows with explicit transcript coordinates now apply as committed transcript state immediately without waiting for hydrate
- committed tool-start events now replace matching transient tool rows instead of being treated as already-applied; rendered ongoing/committed surfaces keep the resolved tool block visible without hydrate once the committed result arrives
- ordinary committed user flushes are now explicitly asserted on the committed ongoing surface without hydrate while later commentary/tool events continue streaming on top
- deferred committed user flushes are now explicitly proved on the rendered ongoing surface: they stay hidden while the assistant is still live, then merge into the committed transcript without hydrate once the assistant final catches up
- the committed-path regression matrix now has targeted coverage for queued user flush, tool start, resolved tool path/finalize, assistant final answer, and reviewer terminal message on the projected-runtime surfaces
- the `conversation_updated` hydration contract is now explicit in tests: plain updates never hydrate, matching committed updates skip hydrate, and only real committed continuity loss takes the committed-conversation sync path
- bootstrap authoritative refresh is now race-covered: a stale bootstrap page cannot overwrite a later committed runtime event that arrived while the refresh was in flight
- concurrent-client interleaving is now race-covered: a hydrating client and a live client converge to the same committed snapshot without duplicate rows when the same committed advancement reaches them through refresh vs live-event paths
- ordinary authoritative hydrates no longer clear/replay the TUI normal-buffer scrollback; only Category C continuity recovery paths are still allowed to rebuild committed ongoing history
- compaction completed/failed no longer synthesize transcript rows before persistence; the persisted `local_entry_added` event is the sole transcript source for compaction notices/errors
- bare committed `conversation_updated` suppression is now replacement-safe: it only collapses into a following rich event when both represent the same committed tail count
- ongoing error refresh no longer relies on broad plain `conversation_updated`; the dedicated `ongoing_error_updated` event now drives authoritative set/clear refresh
- regression coverage exists for the committed-coordinate runtime row path and the compaction single-authority path
- rendered committed-ongoing regression coverage exists for the queued-follow-up visibility/order lifecycle before the assistant-final / queued-user slice
