# Local-First Network Stack Plan

Last updated: 2026-04-19

Authoritative decision source note:

- `docs/dev/decisions.md` is the primary source of truth for locked product/architecture decisions.
- `docs/dev/app-server-migration/spec/locked-decisions.md` mirrors migration-specific locked decisions.
- This tmp plan is an execution handoff and may contain open implementation options; treat only the items explicitly labeled `Locked` here as fixed.

## Why This Exists

Builder's networking stack is currently optimized for correctness and simplicity, not for the real expected topology:

- most sessions are same-machine
- second most common case is nearby network: Docker bridge, VM, Tailscale, LAN
- true WAN / high-latency remote use exists, but is not the primary path

The biggest real problem is still internal RPC churn:

- nearly every unary call redials TCP + WebSocket
- nearly every unary call re-runs handshake
- nearly every scoped unary call re-runs project attach
- subscriptions still own separate connections
- read cancellation is implemented with deadline polling in `shared/client/remote.go`

This document is the corrected execution plan for the next implementation pass.

## Reality Check Against Current Repo

Relevant current code paths:

- internal client transport: `shared/client/remote.go`
- server WS gateway: `server/transport/gateway.go`
- server listener/bootstrap: `server/serve/serve.go`
- daemon addressing/config: `shared/config/config.go`, `shared/config/config_defaults.go`, config registry files
- interactive/headless attach paths: `cli/app/session_server_target.go`, `cli/app/run_prompt_target.go`, `cli/app/remote_server.go`
- UI read cadence and cache behavior: `cli/app/ui_runtime_client.go`
- outbound model HTTP transport: `server/runtimewire/wiring.go`, `server/llm/openai_http.go`

Important implementation facts discovered while reviewing the code:

- current attach/startup flows dial the configured daemon directly; they do not rely on discovery files
- current config schema only models TCP via `server_host` and `server_port`
- there is no existing `network_profile`, `server_transport`, or Unix-socket config surface
- outbound model HTTP pooling is already separate and is not the bottleneck for this work

Implication: the original plan overreached on config/profile policy. The highest-value transport work is still implementable, but it should not start with config churn.

## Locked Decisions

These are the decisions already made for this plan.

### 1. Builder networking is local-first

Network optimization must assume:

- `same-machine` first
- `LAN / near-network` second
- `remote / WAN` third

### 2. The highest-priority fix is persistent internal RPC transport

Do not chase HTTP/3, blanket compression, or speculative WebSocket library replacement before fixing the current per-call redial / re-handshake architecture.

### 3. Transport architecture comes before library migration

We will introduce a narrow Builder-owned transport boundary before changing WebSocket libraries.

That boundary must be protocol-first and app-specific, not a generic "universal websocket wrapper".

### 4. WebSocket library migration is optional, not mandatory

The current `golang.org/x/net/websocket` stack is awkward, but it is not the primary bottleneck. Most gains can be achieved without swapping libraries.

### 5. This pass keeps the current config schema

Do not add `network_profile`, `server_transport`, endpoint blocks, or any other new transport config surface in this pass.

The current `server_host` + `server_port` settings remain the explicit TCP configuration.

### 6. Same-machine optimization should use Unix domain sockets without config churn

On platforms that support Unix domain sockets:

- the daemon should expose a derived local UDS listener in addition to configured TCP
- same-machine clients may prefer UDS
- LAN / remote clients continue to use configured TCP

This is a product decision for implementation direction, not a request for a new user-facing config surface.

### 7. Dual listen is the locked server direction

For the local-first transport phase, the daemon should dual-listen on:

- configured TCP
- derived same-machine UDS, when supported

Dual listen is preferred over auto-picking a single transport because it preserves explicit remote TCP semantics and avoids fragile startup-mode guessing.

### 7.1 UDS path derivation and cleanup contract is locked

For the current local-first transport pass:

- do not introduce any discovery or metadata file for the UDS path
- derive the UDS path deterministically from a runtime-dir base plus a hash of the persistence root and a fixed socket filename
- treat the UDS path as ephemeral same-machine state, not durable config
- on startup, probe and remove only stale socket paths before binding
- on shutdown, best-effort unlink the socket path

### 8. Compression is not a default local optimization

Compression should stay off for same-machine traffic and usually off for LAN traffic. It may be revisited later for large remote unary payloads, but it is not part of this pass.

### 9. HTTP/3 is out of scope for this effort

Current Builder app RPC is WebSocket-based and local-first. HTTP/3 would add complexity and does not address the main bottleneck.

### 10. Outbound model HTTP traffic should keep pooled transports

For OpenAI-compatible or local model backends, keep connection reuse and HTTP/2 enablement where the standard HTTP stack can provide it, but do not confuse that with the internal app RPC problem.

## Non-Goals For This Pass

- no HTTP/3 / QUIC work
- no speculative protocol rewrite away from JSON-RPC over WS
- no transport-profile or `network_profile` config work
- no endpoint/config schema redesign
- no discovery-file revival or discovery-based transport selection
- no blanket compression work
- no giant abstract networking framework

## Proposed Architecture

### A. Builder-owned transport boundary

Introduce a small internal transport package, for example `shared/rpcwire` or `shared/wswire`.

The boundary should model Builder's actual needs:

- dial endpoint
- long-lived duplex connection
- request/response correlation
- notifications/subscriptions
- graceful close
- optional deadlines / cancellation integration

Do not expose raw third-party connection types outside the adapter.

Suggested shape:

- `type Endpoint struct { ... }`
- `type Conn interface { Send(Frame) error; Close(...) error; Events() <-chan Event }`
- `type ClientTransport interface { Dial(ctx, endpoint) (Conn, error) }`
- `type ServerTransport interface { Serve(listener, handler) error }`

The higher-level request mux should remain Builder-owned and sit above this boundary.

### B. Persistent RPC session per `Remote`

Refactor `shared/client/remote.go` so one `Remote` owns one persistent control connection.

Requirements:

- handshake once
- attach project once per scoped remote
- request/response multiplexing over one control connection
- shared reader goroutine
- in-flight request map keyed by request id
- explicit connection lifecycle and close behavior

Important: correctness first. If some subscription classes still need separate managed connections in phase 1, do not block unary persistence on full subscription unification.

### C. Dual-listener daemon topology

Add a derived local UDS listener on supported platforms while preserving the configured TCP listener.

Requirements:

- no config changes required for the user
- TCP remains reachable exactly at configured `server_host` / `server_port`
- UDS path derived from Builder-owned local state such as persistence root / runtime dir
- startup/shutdown clean up stale socket paths safely
- both listeners feed the same in-process server/core/gateway

Important: this is not two servers. It is one server process with two listener fronts.

### D. Local client transport preference

After the transport boundary exists, same-machine clients may prefer the derived UDS path and fall back to configured TCP.

Requirements:

- local optimization only
- no guessing that changes remote semantics
- fallback to TCP must stay explicit and reliable
- client selection logic should stay simple: prefer local UDS only when clearly available; otherwise use configured TCP

### E. Library migration strategy

Do not make migration blocking.

Migration phases:

1. build Builder-owned transport boundary
2. refactor persistent mux on current WS library
3. measure
4. optionally add second adapter spike
5. switch only if the replacement proves clearly better for Builder

## Priority Order

### P0 — Internal RPC architecture

#### P0.1 Persistent unary control channel

Goal: stop redialing/re-handshaking/re-attaching per unary call.

Primary files:

- `shared/client/remote.go`
- `server/transport/gateway.go`
- tests around `shared/client/remote_test.go`, `server/transport/gateway_test.go`, `cli/app/session_server_target_test.go`

Deliverables:

- one persistent control connection per `Remote`
- one read loop
- in-flight request map keyed by request id
- unary request API on top of persistent connection
- connection lifecycle and close semantics documented and tested

Expected payoff:

- largest local/LAN latency win in the whole stack
- fewer transient attach/handshake failures
- lower CPU and kernel/socket churn

#### P0.2 Remove deadline-polling cancellation hack

Goal: eliminate or isolate the `200ms` read-deadline polling loop after the transport refactor.

If the current library remains, reduce the hack surface behind the Builder transport adapter so the rest of the app no longer depends on it.

#### P0.3 Subscription consolidation

Goal: reduce duplicate connection count where safe.

Primary files:

- `shared/client/remote.go`
- `server/transport/gateway.go`
- subscription client packages and tests

Deliverables:

- either keep subscriptions on the same connection when mux complexity stays acceptable
- or replace ad-hoc per-subscription dial logic with a small managed connection set

Note: this is secondary to unary persistence.

### P1 — Builder transport boundary and adapter isolation

#### P1.1 Move third-party WS code behind adapter

Create one package owning raw WS details. The rest of the repo should no longer import third-party WS packages directly.

#### P1.2 Keep request mux Builder-owned

Do not bury request correlation, attach state, or notification routing inside the WS adapter.

The adapter owns frames and socket lifecycle.
Builder owns protocol semantics.

### P2 — Dual-listener daemon

#### P2.1 Add UDS listener alongside configured TCP

Primary files:

- `server/serve/serve.go`
- new listener helper package if needed
- tests around `server/serve/serve_test.go`

Requirements:

- same HTTP/RPC handler tree on both listeners
- stale socket cleanup on restart
- graceful shutdown on both listeners
- Unix-only behavior must stay platform-gated and predictable

#### P2.2 Keep current config as TCP source of truth

Primary files:

- likely no config schema changes
- startup/attach tests should prove existing `server_host` / `server_port` semantics still work

Requirement:

- remote-capable TCP semantics remain unchanged

### P3 — Local client UDS preference

Primary files:

- `shared/client/remote.go` or transport adapter package
- `cli/app/session_server_target.go`
- `cli/app/run_prompt_target.go`
- `cli/app/remote_server.go`

Requirements:

- same-machine client may try UDS first
- TCP fallback must remain reliable
- remote/LAN clients must continue to use configured TCP
- no config churn and no profile heuristics required

### P4 — Optional library migration spike

Only after P0-P3.

Suggested evaluation:

- keep current transport adapter implementation
- add second adapter spike for a maintained alternative if needed
- compare on:
  - implementation complexity
  - cancellation behavior
  - ping/pong and idle handling
  - Unix socket ergonomics
  - write backpressure model
  - testability
  - soak reliability under reconnects

Migration must remain reversible.

## Explicitly Deferred

These are valid future topics, but they are not part of this pass.

- `network_profile`
- operator transport override surface
- endpoint schema redesign
- compression policy by link class
- RTT probing / transport heuristics beyond simple local-availability checks

## Open Implementation Choices

These items are intentionally not locked yet.

### Open: exact transport package shape

The repo should gain a narrow Builder-owned transport boundary, but the final package name and exact interface set remain open.

### Open: exact UDS path location

Locked: use a deterministic runtime-dir path derived from persistence root hash plus a fixed socket filename. Do not use a discovery file to publish the path.

### Open: subscription unification depth in phase 1

It is still open whether all stream classes should share the control connection immediately or whether some should remain on dedicated managed connections in the first landing.

Current recommendation:

- fully fix unary churn first
- unify subscriptions only where the resulting mux stays obviously correct

### Open: exact WebSocket library choice

Current options after the transport abstraction lands:

- keep current `golang.org/x/net/websocket` adapter initially
- spike `gws`
- spike another maintained adapter if later evidence justifies it

Current recommendation:

- do not commit to a replacement library immediately
- make library replacement a post-abstraction decision

### 2026-04-19 `gws` spike result

Spike status:

- `shared/rpcwire` now has an additive `gws` adapter for comparison against the current `golang.org/x/net/websocket` adapter
- default production transport remains `golang.org/x/net/websocket`
- `gws` is not selected by default because Builder-path measurements are mixed rather than clearly better

Correctness checks run for the spike:

- `./scripts/test.sh ./shared/rpcwire ./shared/client`
- added `GWSTransport` round-trip coverage on both TCP and Unix sockets
- added Unix handshake cancellation regression coverage for `GWSTransport`

Benchmark command used:

```bash
go test ./shared/client -run '^$' -bench 'Benchmark(RemoteGetSessionMainViewPersistent|RemoteGetSessionMainViewPersistentGWS|LegacyRedialGetSessionMainView|LegacyRedialGetSessionMainViewGWS|RemoteGetSessionTranscriptPagePersistent|RemoteGetSessionTranscriptPagePersistentGWS|LegacyRedialGetSessionTranscriptPage|LegacyRedialGetSessionTranscriptPageGWS|DialRemoteAttachProjectWorkspace|DialRemoteAttachProjectWorkspaceGWS|RemoteGetSessionMainViewPersistentLocalSocket|RemoteGetSessionMainViewPersistentLocalSocketGWS|RemoteGetSessionTranscriptPagePersistentLocalSocket|RemoteGetSessionTranscriptPagePersistentLocalSocketGWS|DialHandshakeLocalSocket|DialHandshakeLocalSocketGWS|AttachProjectWorkspaceLocalSocket|AttachProjectWorkspaceLocalSocketGWS)$' -benchtime=200x
```

Benchmark summary on `darwin/arm64` (`Apple M1 Pro`):

- TCP persistent `GetSessionMainView`: `x/net` `99076 ns/op`, `gws` `76267 ns/op`
- TCP persistent transcript page: `x/net` `48444 ns/op`, `gws` `45464 ns/op`
- TCP legacy redial `GetSessionMainView`: `x/net` `286123 ns/op`, `gws` `292442 ns/op`
- TCP legacy redial transcript page: `x/net` `274162 ns/op`, `gws` `276908 ns/op`
- TCP dial + handshake + attach: `x/net` `237833 ns/op`, `gws` `238413 ns/op`
- Unix-socket persistent `GetSessionMainView`: `x/net` `36631 ns/op`, `gws` `36165 ns/op`
- Unix-socket persistent transcript page: `x/net` `25410 ns/op`, `gws` `22907 ns/op`
- Unix-socket dial + handshake: `x/net` `104013 ns/op`, `gws` `107987 ns/op`
- Unix-socket attach RPC after handshake: `x/net` `18383 ns/op`, `gws` `17064 ns/op`

Transport-only bridge benchmark command used:

```bash
go test ./shared/rpcwire -run '^$' -bench 'Benchmark(TransportRoundTripTCP|TransportRoundTripTCPGWS|TransportRoundTripLocalSocket|TransportRoundTripLocalSocketGWS|TransportDialTCP|TransportDialTCPGWS|TransportDialLocalSocket|TransportDialLocalSocketGWS)$' -benchtime=300x
```

Transport-only bridge summary:

- raw transport persistent round-trip over TCP: `x/net` `82512 ns/op`, `gws` `41409 ns/op`
- raw transport persistent round-trip over Unix socket: `x/net` `14174 ns/op`, `gws` `11436 ns/op`
- raw transport dial over TCP: `x/net` `124806 ns/op`, `gws` `128240 ns/op`
- raw transport dial over Unix socket: `x/net` `64063 ns/op`, `gws` `75836 ns/op`

Interpretation of the discrepancy vs upstream `gws` benchmarks:

- upstream `gws` benchmarks primarily isolate frame encode/decode internals and often avoid real socket write cost entirely
- Builder transport-only numbers already compress that gap to about `2x` on the hot TCP round-trip path and below that on Unix sockets
- once Builder JSON-RPC, handshake, attach, mux, and app decode costs are included, the remaining gain compresses further
- conclusion: the upstream benchmark is not evidence of a `40x` end-to-end Builder win; it is evidence that `gws` has a faster internal frame path

Interpretation:

- the biggest shipped win still comes from the persistent control connection and same-machine Unix socket path, not the library swap
- `gws` improves steady-state persistent unary calls and reduces allocations there
- `gws` does not improve dial/handshake enough, and full attach-path cost stays effectively flat
- current decision: keep `gws` as a spike/additive adapter only; revisit default switch only after broader parity coverage and more Builder-path measurements

## Validation Plan

Each phase must be validated with both correctness tests and simple latency measurements.

### Must-have tests

- persistent `Remote` survives many unary calls without reconnecting
- unary requests remain correctly correlated under concurrency
- reconnect path rehydrates safely
- subscriptions do not cross-deliver events
- daemon can serve the same RPC surface on both TCP and UDS when supported
- stale UDS path cleanup works across restart scenarios
- local client UDS preference falls back cleanly to TCP
- configured TCP attach behavior remains unchanged

### Must-have measurements

Measure before and after for at least:

- repeated `GetSessionMainView`
- repeated transcript page reads
- repeated process list reads
- startup attach latency

Measure at least:

- same-machine over TCP before persistent mux
- same-machine over TCP after persistent mux
- same-machine over UDS after local transport support lands

### Suggested benchmark harness

- lightweight benchmark or integration command hitting live daemon
- N repeated unary requests over attached remote
- log median / p95 / connection count / handshake count

## Risks And Watchouts

- request mux bugs can introduce subtle cross-request corruption; keep correlation strongly typed and heavily tested
- reconnect logic can accidentally hide correctness issues by returning stale cached state; rehydrate from server truth after continuity loss
- UDS path ownership/cleanup is platform-sensitive
- trying to make client transport selection too smart will create more fragility than value; keep local preference simple
- WS library swap done too early will hide architecture problems instead of solving them

## Recommended Implementation Sequence

1. Introduce Builder-owned transport boundary without changing behavior.
2. Refactor `Remote` to persistent unary connection + request mux.
3. Remove or isolate the deadline-polling cancellation hack.
4. Consolidate subscription transport only where correctness remains obvious.
5. Add dual-listener server support: configured TCP + derived UDS.
6. Add local client UDS preference with clean TCP fallback.
7. Run optional WebSocket library spike behind the abstraction boundary.

## Recommendation To The Implementer

Do not start with HTTP/2, HTTP/3, compression, or config redesign.

Start with `shared/client/remote.go` and the request lifecycle. That is where the largest real local/LAN win sits.

After that is correct, add dual-listener support and only then teach local clients to prefer UDS.

Keep remote semantics boring and explicit: configured TCP remains the durable remote path.
