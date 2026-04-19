# Phase 9 Follow-Up: Multi-Client Session Control

Status: deferred follow-up after the current-TUI single-controller shipping path

## Purpose

This document makes the temporary same-session single-controller restriction explicit and defines the concrete work required to remove it safely.

The current server path intentionally ships with this temporary simplification:

- multiple clients may attach and read the same session
- exactly one client may control or mutate that session at a time

That restriction is currently enforced through controller-lease-gated mutating APIs and runtime/session services.

This file exists so future work does not have to infer the intended lift from scattered “temporary” wording.

## Product Target After This Follow-Up

When this follow-up is complete, the product target returns to the original multi-client control model:

- multiple attached clients may issue mutating requests against the same session
- the server remains the sole ordering authority
- per-session ordering is deterministic and server-authored rather than delegated to one controlling client
- any attached client with access to the session may answer asks or approvals
- retry behavior remains idempotent and deterministic across loopback and transport paths

This is not collaborative text editing. It is concurrent server-authoritative session control.

## Restrictions Lifted By This Follow-Up

The following temporary restrictions should be removed only when the prerequisites below are satisfied.

### 1. Controller lease stops being the general mutation gate

Current temporary behavior:

- most mutating runtime/session/prompt-control operations require `controller_lease_id`
- attached non-controller clients are rejected for mutations

Target behavior:

- ordinary mutating requests do not require one client to hold exclusive control over the session
- the server serializes those requests authoritatively per session
- runtime residency/activation leases remain separate from mutation ordering if they are still needed for lifecycle or disconnect cleanup

### 2. Non-controller prompt responses become valid

Current temporary behavior:

- ask and approval answers require the controller lease

Target behavior:

- any attached client with session access may answer asks or approvals
- the first committed authoritative answer wins
- later contenders receive a deterministic already-resolved result

### 3. Same-session mutating races become first-class server behavior

Current temporary behavior:

- mutating races are mostly prevented by rejecting all but the controlling client

Target behavior:

- concurrent prompt submission, interrupt, process control, session lifecycle mutations, runtime setting changes, and prompt responses are defined server behaviors
- the server owns final ordering and emits resulting authoritative state transitions

## Current Controller-Lease-Gated RPC Inventory

This is the current serverapi/protocol surface that still depends on `controller_lease_id` and therefore cannot yet participate in true server-serialized same-session multi-client control.

### Must lose controller-lease gating before this follow-up can be called complete

#### Runtime mutation family

Current contract source: `shared/serverapi/runtime_control.go`

- `runtime.setSessionName`
- `runtime.setThinkingLevel`
- `runtime.setFastModeEnabled`
- `runtime.setReviewerEnabled`
- `runtime.setAutoCompactionEnabled`
- `runtime.appendLocalEntry`
- `runtime.submitUserMessage`
- `runtime.submitUserShellCommand`
- `runtime.compactContext`
- `runtime.compactContextForPreSubmit`
- `runtime.submitQueuedUserMessages`
- `runtime.interrupt`
- `runtime.queueUserMessage`
- `runtime.discardQueuedUserMessagesMatching`
- `runtime.recordPromptHistory`

#### Session lifecycle mutation family

Current contract source: `shared/serverapi/session_lifecycle.go`

- `session.persistInputDraft`
- `session.resolveTransition` when acting on an existing attached session

#### Prompt-control mutation family

Current contract source: `shared/serverapi/prompt_control.go`

- `ask.answer`
- `approval.answer`

### Must be reviewed during the lift, but do not necessarily lose runtime-lease semantics

#### Runtime activation family

Current contract source: `shared/serverapi/session_runtime.go`

- `session.runtime.activate`
- `session.runtime.release`

These methods may still keep `lease_id` semantics after the controller gate is removed, but the lease must stop meaning “the one client allowed to mutate the session.”

### Already not controller-lease-gated, but still part of the multi-client control proof surface

#### Process control family

Current contract source: `shared/serverapi/process_view.go`

- `process.kill`
- `process.inlineOutput`

These methods already avoid `controller_lease_id`, but they still need deterministic same-session concurrent control semantics under the final server-serialized model.

## RPC Migration Checklist

Treat this as the concrete checklist for removing the restriction.

- [ ] runtime mutation family no longer validates or requires `controller_lease_id`
- [ ] session lifecycle mutation family no longer uses controller ownership as the write gate
- [ ] prompt-control mutation family no longer requires the controlling client
- [ ] runtime activation family has narrowed lease semantics documented separately from mutation authorization
- [ ] client-facing docs and protocol comments no longer imply one controlling client is required for ordinary session writes

## Required Prerequisites Before Lifting The Restriction

Do not remove the single-controller guard before all of the following are present.

### A. Every mutating API has a real idempotency contract

Must exist:

- all mutating protocol methods carry `client_request_id`
- idempotency scope is documented as `(method, resource identity, client_request_id)` or an equivalent explicit contract
- payload mismatch on reused request ids rejects deterministically
- cancellation/timeout does not become cached success
- the same retry semantics hold in loopback and remote transport

Why this is required:

- once multiple clients can mutate the same session, retries and duplicate delivery stop being edge cases and become normal race conditions

### B. Deduplication state is durable and shared across the full write surface

Must exist:

- one persisted dedup store/table with explicit retention policy
- coverage across the full TUI-relevant and prompt-response mutation surface, not only prompt-submit slices
- no remaining reliance on per-process in-memory dedup registries as the authoritative protection for session mutations

Why this is required:

- a multi-client write model cannot depend on one process-local cache to preserve correctness

### C. Server-owned per-session mutation serialization is explicit

Must exist:

- one clear per-session mutation ordering model in server-owned services
- no reliance on client-held exclusivity to prevent conflicting operations
- explicit busy/reject/defer rules for overlapping run-affecting mutations
- explicit rules for which reads stay available while writes serialize

Why this is required:

- lifting the gate without a real ordering model would turn hidden races into product bugs

### D. Ask/approval semantics are promoted to the multi-client contract

Must exist:

- ask/approval answer methods no longer depend on the controller lease
- deterministic first-wins semantics are covered for concurrent answers from different clients
- attach/reconnect hydration and live prompt delivery remain consistent under those races

Why this is required:

- the original product model explicitly allows any attached client to answer guarded prompts

### E. Runtime activation lease semantics are decoupled from control exclusivity

Must exist:

- a clear statement of what `lease_id` still means after control exclusivity is removed
- if runtime leases remain, they describe runtime residency/lifecycle rather than “the one allowed writer”
- reconnect/disconnect cleanup semantics remain explicit and testable

Why this is required:

- today the same session-runtime machinery is used to enforce single-controller semantics

### F. Acceptance proof for cross-client mutation races exists

Must exist in automated tests:

- two clients submit prompts concurrently to the same session and observe deterministic ordering/outcomes
- one client interrupts while another submits follow-up work and the resulting order is explicit
- one client changes runtime settings while another submits work and the observed run behavior is defined
- two clients race ask responses and approval responses with deterministic first-wins results
- two clients issue process-control actions with deterministic outcomes
- loopback and remote transport paths obey the same mutation-ordering and retry rules

Why this is required:

- without black-box race proof, the restriction would be lifted on assumption rather than on contract

## Acceptance Matrix By Mutation Family

The follow-up should not be marked complete on generic “multi-client works” wording alone. Each family needs explicit proof.

### 1. Runtime mutation family

Minimum proof:

- two clients concurrently call `runtime.submitUserMessage` and observe deterministic server ordering
- one client calls `runtime.interrupt` while another submits or drains queued work and the final outcome is explicit
- one client changes `runtime.setThinkingLevel` / `runtime.setFastModeEnabled` / `runtime.setReviewerEnabled` while another submits work and the active/following run behavior matches the documented ordering rule
- one client queues or discards queued work while another submits or interrupts and the queue/result state is deterministic
- loopback and remote transport produce the same ordered outcomes and retry semantics

### 2. Session lifecycle mutation family

Minimum proof:

- concurrent `session.persistInputDraft` writes from two clients follow an explicit last-writer or server-order rule
- `session.resolveTransition` races against runtime mutations produce deterministic attach/fork/logout/new-session outcomes
- retry/idempotency on transition mutations remains correct when two clients submit competing requests

### 3. Prompt-control mutation family

Minimum proof:

- two clients race `ask.answer` and first committed answer wins deterministically
- two clients race `approval.answer` and first committed answer wins deterministically
- losing responders receive a deterministic already-resolved result rather than silent success or stale rejection
- attach/reconnect hydration plus prompt activity converge to the same final resolved prompt state on both clients

### 4. Process control family

Minimum proof:

- two clients race `process.kill` on the same process and final outcome is deterministic and idempotent
- one client calls `process.inlineOutput` while another kills the process and the resulting output/state contract is explicit
- process control ordering remains consistent with owning session/run state under loopback and remote transport

### 5. Runtime activation family

Minimum proof:

- removing controller exclusivity does not require reclaiming an old `lease_id` to continue writing
- reconnect/disconnect still preserves runtime residency cleanup semantics
- duplicate `session.runtime.activate` / `session.runtime.release` retries remain deterministic without reintroducing one-writer semantics through the back door

## Concrete Workstreams

### 9A. Mutation Contract Cleanup

- add `client_request_id` to all remaining mutating RPCs that still rely only on `controller_lease_id`
- document the exact idempotency scope and retention contract
- unify mismatch/cancellation semantics across mutation families

### 9B. Persisted Dedup Store

- land one authoritative dedup persistence model
- migrate remaining in-memory dedup slices onto it
- add retention cleanup and observability around replayed vs mismatched requests

### 9C. Server Serialization Model

- define one per-session mutation arbitration path that replaces exclusive client ownership
- keep the busy-state contract explicit for run-affecting operations
- ensure lifecycle/process/prompt-control mutations all pass through the same ordering model or a clearly compatible split model

### 9D. Prompt-Control Reopening

- remove controller-lease gating from ask/approval answers
- preserve deterministic already-resolved behavior for later responders
- extend prompt-resource and prompt-activity tests to multi-client answer races

### 9E. Lease Semantics Cleanup

- narrow `lease_id` semantics to runtime activation/lifecycle if still needed
- document whether session attach alone is sufficient for ordinary writes after server-side ordering exists
- prove disconnect/reconnect does not rely on reclaiming an old writer identity

### 9F. Acceptance And Rollout Proof

- add loopback and remote black-box coverage for concurrent session control
- prove the server, not clients, defines final ordering
- prove no product workflow regresses when the temporary exclusivity guard is removed

## Exit Criteria

This follow-up is complete only when all of the following are true.

- no ordinary session mutation requires a client-exclusive controller lease
- all mutating APIs used by attached clients carry the documented `client_request_id` contract
- deduplication and retry behavior are persisted and deterministic across the full retained write surface
- any attached client with session access can answer asks or approvals
- same-session concurrent control races are covered by black-box loopback and remote tests
- the active migration docs no longer describe single-controller semantics as the product contract

## Non-Goals

- collaborative draft editing or merged text-input UX
- multi-user authz/tenant semantics
- speculative GUI collaboration behavior beyond server-authoritative session control
- relaxing single-server-per-persistence-root safeguards

## Relationship To Current Phase 2 Shipping Work

The current Phase 2 residual path is allowed to keep the temporary single-controller restriction because it is optimizing for the current TUI shipping path.

This follow-up begins only after that path no longer depends on controller exclusivity as a scope-management tool.

Until then:

- single-controller semantics remain intentional
- future agents should not treat “temporary” as permission to remove the restriction without the prerequisites in this file
