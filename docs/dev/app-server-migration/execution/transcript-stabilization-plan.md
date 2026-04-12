# Transcript Stabilization Plan

## Goal

Stop the realtime transcript regressions on `app-server-integration` and restore confidence in active-session UX before more migration work proceeds.

This plan is intentionally execution-focused. It locks the work to be done and the order to do it in, without prescribing low-level implementation details.

## Scope

In scope:

- active-session ongoing-mode transcript correctness
- detail-mode transcript correctness for committed history
- reconnect and hydration behavior for active and dormant sessions
- consistency between loopback and remote session-activity paths
- rendered TUI behavior, not only internal state

Out of scope for this plan:

- new product features
- protocol redesign beyond what is needed for transcript correctness
- unrelated performance work outside transcript/state handling
- broader phase 4 work and phase 5B multi-client proof work

## Stabilization Rule

Until this plan is complete, transcript-affecting work must be treated as stabilization work, not normal feature work.

- Do not stack unrelated migration changes on top of transcript/runtime UX changes.
- [x] Require rendered-path proof for any change that affects ongoing or detail transcript behavior.
- [x] Treat loopback and remote behavior as equally important acceptance surfaces.

## Workstream 1: State Ownership

- [x] Define and document one authoritative path for live active-session transcript updates.
- [x] Define and document the role of hydration reads as recovery/repair rather than primary live UX.
- [x] Audit the CLI for every place that can mutate transcript-visible state.
- [x] Remove or consolidate overlapping state update paths that can overwrite each other.
- [x] Establish a clear rule for when transcript reads may replace current UI state and when they must not.

Exit criteria:

- [x] There is a single documented answer to "what updates ongoing mode live?"
- [x] There is a single documented answer to "what can replace already-visible transcript state?"

## Workstream 2: Live vs Committed State Separation

- [x] Separate stabilization work around committed transcript state from ephemeral live state.
- [x] Audit which visible elements are durable and which are transient.
- [x] Eliminate cases where recovery/hydration logic clears or rewrites transient live state incorrectly.
- [x] Eliminate cases where transient state is being treated as if it were committed durable transcript state.

Exit criteria:

- [x] Committed transcript recovery works without depending on transient live state.
- [x] Live commentary/tool preview behavior works without depending on transcript rehydrate.

## Workstream 3: Ordering and Freshness

- [x] Introduce an explicit ordering/freshness contract between live events and transcript reads.
- [x] Ensure the client can distinguish newer live state from older hydration state.
- [x] Ensure reconnect and reload behavior can converge safely without clearing newer visible activity.
- [x] Ensure dormant-session hydration and active-session hydration follow the same correctness rules.

Exit criteria:

- [x] A stale read cannot erase newer live transcript state.
- [x] Reconnect behavior is predictable and documented.

## Workstream 4: Test Coverage That Matches The Product

- [x] Add rendered TUI regression tests for the full active-session flow.
- [x] Add real migrated-boundary tests for remote/session-activity delivery.
- [x] Cover ordering of mixed event classes, not just isolated events.
- [x] Cover stale-read vs live-stream race scenarios.
- [x] Cover reconnect/hydrate correctness for active sessions.
- [x] Cover dormant transcript reopen correctness for committed history.

Minimum required scenarios:

- [x] user message appears immediately in ongoing mode
- [x] assistant commentary appears live
- [x] tool call appears live in order
- [x] tool result appears in order
- [x] final answer appears in order
- [x] reconnect rehydrates committed transcript without blanking the session
- [x] stale transcript reads cannot wipe newer live state
- [ ] loopback and remote paths behave equivalently for transcript-critical flows

Exit criteria:

- [x] The user-reported failure modes are covered by automated tests.
- [x] Transcript-affecting fixes are blocked on rendered-path proof, not only unit tests.

## Workstream 5: Observability And Debugging

- [x] Add enough diagnostics to explain why visible transcript state changed.
- [x] Make it easy to tell whether a visible change came from a live event, a transcript read, or a recovery path.
- [x] Make it easy to identify ordering/freshness mismatches during debugging.
- [x] Add targeted debugging guidance for future transcript regressions.

Exit criteria:

- [x] Transcript regressions can be diagnosed from logs/state transitions without guesswork.
- [x] It is possible to tell whether a failure is in server emission, transport, hydration, or frontend apply logic.

## Workstream 6: Regression Triage And Cleanup

- [x] Inventory all currently known transcript-related regressions on `app-server-integration`.
- [x] Group them by root cause rather than by symptom.
- [x] Close quick symptom patches that do not fit the stabilized model.
- [x] Re-test previously fixed regressions after each structural transcript change.
- [x] Keep a running checklist of transcript-critical user workflows and their current status.

Exit criteria:

- [x] Transcript bug backlog is organized by cause and current status.
- [x] No open transcript regression is being deferred silently.

## Sequencing

Recommended execution order:

1. State ownership
2. Live vs committed state separation
3. Ordering and freshness
4. Test coverage that matches the product
5. Observability and debugging
6. Regression triage and cleanup

Parallelizable work:

- [x] rendered TUI regression tests
- [x] remote/session-activity regression tests
- [x] transcript mutation-path inventory
- [x] regression backlog inventory
- [x] observability/debugging additions

Must stay serialized:

- changing transcript state ownership rules
- changing hydration overwrite rules
- changing ordering/freshness semantics

## Completion Criteria

This plan is complete only when all of the following are true:

- [ ] Active ongoing mode is reliable under normal work, not only after reload.
- [x] Detail mode and dormant-session reopen preserve committed transcript correctly.
- [x] Reconnect/hydration behavior no longer causes visible transcript loss.
- [ ] The same transcript-critical scenarios pass on both loopback and remote paths.
- [x] We have rendered-path automated proof for the key user-facing flows.
- [x] New transcript regressions are easier to localize than they are today.

## Release Gate

Do not treat transcript stabilization as done just because individual bugs were fixed.

- [ ] Keep broader phase-4 work and phase-5B multi-client proof work blocked if transcript correctness is still drifting.
- [ ] Only resume broader migration execution once the completion criteria above are satisfied.

## Current Gaps

The current highest-value remaining gaps from the parallel audit are:

- [x] remote/session-activity proof for `user -> commentary/progress -> tool call -> tool result -> final answer`
- [x] active-session reconnect/hydration rendered proof on the migrated path
- [x] explicit freshness/overwrite rule for live events vs transcript reads
- [x] focused dormant-session reopen proof for committed transcript in both modes

Known remaining caveat:

- [ ] remote session-activity still preserves live assistant progress via `assistant_delta`, but does not yet surface the persisted commentary transcript entry for the assistant/tool-call turn

Deferred decision for this slice:

- [x] Defer raw remote commentary-entry parity for assistant/tool-call turns until a later runtime-event contract change.
- [x] Treat the current requirement as convergence via hydrate, not event-for-event parity on the raw session-activity stream.

Cleanup proof for the checked regression-triage item lives in:

- `docs/dev/app-server-migration/analysis/transcript-cleanup-audit.md`

## Current Workflow Status

This is the running workflow checklist for stabilization triage. It is intentionally narrower than the release gate.

- [x] ordinary user submit shows in ongoing mode immediately
- [x] assistant commentary stays visible after user flush instead of being dropped by hydrate
- [x] loopback ongoing-mode mixed event flow renders in order
- [x] transcript hydrate after `conversation_updated` restores committed transcript without replay duplication
- [x] stale/same-revision hydrate cannot wipe newer live assistant output
- [x] stale/same-revision hydrate cannot wipe newer live reasoning output
- [x] dormant-session reopen preserves committed transcript
- [ ] remote path carries the same assistant commentary transcript entry shape as loopback

Authoritative proof for the checked items lives in:

- `docs/dev/app-server-migration/analysis/transcript-workflow-proof.md`
- `docs/dev/app-server-migration/analysis/transcript-observability-plan.md`
- `docs/dev/app-server-migration/analysis/transcript-overlap-audit.md`

## Remaining Execution Slices

The plan is no longer blocked on broad ownership/freshness design or cleanup implementation work.

No further mandatory code slices remain inside this stabilization execution plan.

What remains is explicit and non-hidden:

- the remote commentary gap stays deferred until we deliberately change the runtime-event/projection contract
- release-gate confidence for entering phase 5A implementation remains a product decision rather than an implementation checklist item

Ongoing maintenance while the branch evolves:

1. Re-run the transcript-critical workflow matrix after each structural change and keep this checklist current.

## Orchestrator Notes

These items are intentionally not treated as open design questions anymore.

- The workflow-proof mapping is complete enough to support the current checklist. Update it whenever a checklist item changes.
- The observability slice is now concretely scoped; future implementation should follow `analysis/transcript-observability-plan.md` instead of inventing ad hoc logging.
- The remote commentary gap is an intentional defer, not a forgotten bug. It should only be reopened alongside a deliberate runtime-event contract change.
