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
- phase 4 and phase 5 feature execution

## Stabilization Rule

Until this plan is complete, transcript-affecting work must be treated as stabilization work, not normal feature work.

- [ ] Do not stack unrelated migration changes on top of transcript/runtime UX changes.
- [ ] Require rendered-path proof for any change that affects ongoing or detail transcript behavior.
- [ ] Treat loopback and remote behavior as equally important acceptance surfaces.

## Workstream 1: State Ownership

- [x] Define and document one authoritative path for live active-session transcript updates.
- [x] Define and document the role of hydration reads as recovery/repair rather than primary live UX.
- [x] Audit the CLI for every place that can mutate transcript-visible state.
- [ ] Remove or consolidate overlapping state update paths that can overwrite each other.
- [ ] Establish a clear rule for when transcript reads may replace current UI state and when they must not.

Exit criteria:

- [ ] There is a single documented answer to "what updates ongoing mode live?"
- [ ] There is a single documented answer to "what can replace already-visible transcript state?"

## Workstream 2: Live vs Committed State Separation

- [ ] Separate stabilization work around committed transcript state from ephemeral live state.
- [ ] Audit which visible elements are durable and which are transient.
- [ ] Eliminate cases where recovery/hydration logic clears or rewrites transient live state incorrectly.
- [ ] Eliminate cases where transient state is being treated as if it were committed durable transcript state.

Exit criteria:

- [ ] Committed transcript recovery works without depending on transient live state.
- [ ] Live commentary/tool preview behavior works without depending on transcript rehydrate.

## Workstream 3: Ordering and Freshness

- [ ] Introduce an explicit ordering/freshness contract between live events and transcript reads.
- [ ] Ensure the client can distinguish newer live state from older hydration state.
- [ ] Ensure reconnect and reload behavior can converge safely without clearing newer visible activity.
- [ ] Ensure dormant-session hydration and active-session hydration follow the same correctness rules.

Exit criteria:

- [ ] A stale read cannot erase newer live transcript state.
- [ ] Reconnect behavior is predictable and documented.

## Workstream 4: Test Coverage That Matches The Product

- [x] Add rendered TUI regression tests for the full active-session flow.
- [x] Add real migrated-boundary tests for remote/session-activity delivery.
- [ ] Cover ordering of mixed event classes, not just isolated events.
- [x] Cover stale-read vs live-stream race scenarios.
- [x] Cover reconnect/hydrate correctness for active sessions.
- [x] Cover dormant transcript reopen correctness for committed history.

Minimum required scenarios:

- [ ] user message appears immediately in ongoing mode
- [ ] assistant commentary appears live
- [ ] tool call appears live in order
- [ ] tool result appears in order
- [ ] final answer appears in order
- [ ] reconnect rehydrates committed transcript without blanking the session
- [ ] stale transcript reads cannot wipe newer live state
- [ ] loopback and remote paths behave equivalently for transcript-critical flows

Exit criteria:

- [ ] The user-reported failure modes are covered by automated tests.
- [ ] Transcript-affecting fixes are blocked on rendered-path proof, not only unit tests.

## Workstream 5: Observability And Debugging

- [ ] Add enough diagnostics to explain why visible transcript state changed.
- [ ] Make it easy to tell whether a visible change came from a live event, a transcript read, or a recovery path.
- [ ] Make it easy to identify ordering/freshness mismatches during debugging.
- [ ] Add targeted debugging guidance for future transcript regressions.

Exit criteria:

- [ ] Transcript regressions can be diagnosed from logs/state transitions without guesswork.
- [ ] It is possible to tell whether a failure is in server emission, transport, hydration, or frontend apply logic.

## Workstream 6: Regression Triage And Cleanup

- [x] Inventory all currently known transcript-related regressions on `app-server-integration`.
- [x] Group them by root cause rather than by symptom.
- [ ] Close quick symptom patches that do not fit the stabilized model.
- [ ] Re-test previously fixed regressions after each structural transcript change.
- [ ] Keep a running checklist of transcript-critical user workflows and their current status.

Exit criteria:

- [ ] Transcript bug backlog is organized by cause and current status.
- [ ] No open transcript regression is being deferred silently.

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
- [ ] remote/session-activity regression tests
- [x] transcript mutation-path inventory
- [x] regression backlog inventory
- [ ] observability/debugging additions

Must stay serialized:

- [ ] changing transcript state ownership rules
- [ ] changing hydration overwrite rules
- [ ] changing ordering/freshness semantics

## Completion Criteria

This plan is complete only when all of the following are true:

- [ ] Active ongoing mode is reliable under normal work, not only after reload.
- [ ] Detail mode and dormant-session reopen preserve committed transcript correctly.
- [ ] Reconnect/hydration behavior no longer causes visible transcript loss.
- [ ] The same transcript-critical scenarios pass on both loopback and remote paths.
- [ ] We have rendered-path automated proof for the key user-facing flows.
- [ ] New transcript regressions are easier to localize than they are today.

## Release Gate

Do not treat transcript stabilization as done just because individual bugs were fixed.

- [ ] Keep phase-4/5 feature work blocked if transcript correctness is still drifting.
- [ ] Only resume broader migration execution once the completion criteria above are satisfied.

## Current Gaps

The current highest-value remaining gaps from the parallel audit are:

- [x] remote/session-activity proof for `user -> commentary/progress -> tool call -> tool result -> final answer`
- [x] active-session reconnect/hydration rendered proof on the migrated path
- [ ] explicit freshness/overwrite rule for live events vs transcript reads
- [x] focused dormant-session reopen proof for committed transcript in both modes

Known remaining caveat:

- [ ] remote session-activity still preserves live assistant progress via `assistant_delta`, but does not yet surface the persisted commentary transcript entry for the assistant/tool-call turn
