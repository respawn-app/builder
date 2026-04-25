# `events.jsonl` Schema V2

Status: proposal for issue #130

## Goal

Replace the current `kind + opaque payload` event log with a small typed event union that:

- preserves current replay semantics
- stops persisting duplicate transcript/projection data
- keeps append-only JSONL durability
- stays easy to migrate from v1 in a streaming rewrite

## Audit Snapshot

Audited session: `8f0491f5-883a-4d1c-89ab-7e24e09b67c9`

The session was live while being inspected, so the audit uses a frozen copy taken on `2026-04-19` from:

- `~/.builder/projects/project-94b18685-19ed-4513-96bb-bcffa10410ff/sessions/8f0491f5-883a-4d1c-89ab-7e24e09b67c9/events.jsonl`

Notes:

- Post-migration sessions do not have `session.json` anymore. SQLite owns session metadata. The artifact directory only contains `events.jsonl` and `steps.log`.
- Frozen copy size: `1,752,226` bytes
- Event count: `586`

### Repro Commands

Freeze the live session first so numbers stop drifting during inspection:

```sh
cp ~/.builder/projects/project-94b18685-19ed-4513-96bb-bcffa10410ff/sessions/8f0491f5-883a-4d1c-89ab-7e24e09b67c9/events.jsonl /tmp/builder-issue130-8f0491f5-events.jsonl
wc -c /tmp/builder-issue130-8f0491f5-events.jsonl
```

Main audit query:

```sh
jq -s '
  def bytes: (tojson|utf8bytelength);
  {
    count: length,
    total_bytes: (map(bytes)|add),
    by_kind: (group_by(.kind)|map({kind: .[0].kind, count:length, total_bytes:(map(bytes)|add), avg_bytes:((map(bytes)|add)/length|floor)})|sort_by(-.total_bytes)),
    message_by_role: ([.[]|select(.kind=="message")]|group_by(.payload.role)|map({role: .[0].payload.role, count:length, total_bytes:(map(bytes)|add)})|sort_by(-.total_bytes))
  }
' /tmp/builder-issue130-8f0491f5-events.jsonl
```

Redundant tool-result family query:

```sh
jq -s '
  [ .[] | select(.kind=="message" and .payload.role=="tool") | {seq, step_id, tool_call_id:(.payload.tool_call_id//""), content_bytes:(.payload.content|tojson|utf8bytelength), event_bytes:(tojson|utf8bytelength)} ] as $toolmsgs
  | [ .[] | select(.kind=="tool_completed") | {seq, step_id, call_id:(.payload.call_id//""), output_bytes:(.payload.output|tojson|utf8bytelength), event_bytes:(tojson|utf8bytelength)} ] as $completions
  | [ $toolmsgs[] as $m | $completions[] | select(.call_id==$m.tool_call_id and .step_id==$m.step_id) ] as $pairs
  | {
      tool_message_count: ($toolmsgs|length),
      tool_completed_count: ($completions|length),
      paired_count: ($pairs|length),
      tool_message_content_bytes: ($toolmsgs|map(.content_bytes)|add),
      tool_completed_output_bytes: ($completions|map(.output_bytes)|add),
      tool_message_event_bytes: ($toolmsgs|map(.event_bytes)|add),
      tool_completed_event_bytes: ($completions|map(.event_bytes)|add)
    }
' /tmp/builder-issue130-8f0491f5-events.jsonl
```

### Size By Event Kind

| Kind | Count | Bytes | Share |
| --- | ---: | ---: | ---: |
| `message` | 245 | 1,104,802 | 63.3% |
| `tool_completed` | 153 | 570,903 | 32.7% |
| `cache_response_observed` | 85 | 33,986 | 1.9% |
| `cache_request_observed` | 85 | 28,881 | 1.7% |
| `local_entry` | 5 | 3,797 | 0.2% |
| `run_started` + `run_finished` | 6 | 2,014 | 0.1% |
| `prompt_history` | 6 | 740 | <0.1% |
| `cache_warning` | 1 | 257 | <0.1% |

### `message` Breakdown

| Role | Count | Bytes | Share Of Whole File |
| --- | ---: | ---: | ---: |
| `tool` | 153 | 609,310 | 34.9% |
| `assistant` | 83 | 475,990 | 27.3% |
| `developer` | 6 | 18,901 | 1.1% |
| `user` | 3 | 601 | <0.1% |

### Biggest Redundant Family

The file contains:

- `153` `message` events with `role=tool`
- `153` `tool_completed` events
- all `153` pair cleanly on `step_id + call_id`

Measured payload sizes for the paired family:

- `message.role=tool` content bytes: `576,003`
- `tool_completed.output` bytes: `536,987`

Measured whole-event sizes for the paired family:

- `message.role=tool` event bytes: `609,310`
- `tool_completed` event bytes: `570,903`

This is the same tool result persisted twice in different shapes:

- once as raw tool output for request reconstruction (`tool_completed`)
- again as a stringified tool message (`message.role=tool`)

Removing the `message.role=tool` mirror alone would save about `609 KB` of whole-event bytes, or about `35%` of this sample file, before any other cleanup.

### Other Structural Smells

- `message` is a catch-all for unrelated payload families: user input, developer injections, assistant commentary/final answers, assistant tool calls, reasoning items, and tool-result mirrors.
- `run_started` / `run_finished` repeat `step_id` both in the envelope and in the payload.
- replay currently joins data across unrelated kinds (`message` + `tool_completed`) to reconstruct one logical tool exchange.
- transcript-facing restore consumes a mix of provider-history events and already-projected local rows.

## Minimal V2 Contract

V2 should keep one append-only JSONL file, but switch to:

1. a file header line declaring schema version
2. a single typed event envelope
3. a closed event-type union with typed payload structs
4. exactly one persisted representation for each durable semantic fact

### File Shape

Line 1 is a header, not a transcript event:

```json
{"type":"header","schema":"builder.events","version":2}
```

All following lines use one event envelope:

```json
{"seq":12,"ts":"2026-04-19T21:53:24.123456Z","step_id":"step-1","type":"tool.result","data":{"call_id":"call_123","tool_name":"shell","is_error":false,"output":{"exit_code":0,"output":"/tmp\n","truncated":false}}}
```

Envelope fields:

- `seq`: monotonically increasing event sequence
- `ts`: event timestamp
- `step_id`: optional step scope owned by the envelope only
- `type`: event discriminant
- `data`: typed payload selected by `type`

Rules:

- no opaque `payload` blob
- payloads must not repeat `seq`, `ts`, `step_id`, or `type`
- unknown `type` must fail decode loudly

## Event Union

This is the smallest v2 set that preserves current replay behavior.

### `prompt_history.append`

Used by `session.ReadPromptHistory()`.

```json
{"text":"Take and address https://github.com/respawn-app/builder/issues/130"}
```

### `run.started`

```json
{"run_id":"run-1","started_at":"2026-04-19T21:53:23.020236Z"}
```

### `run.finished`

```json
{"run_id":"run-1","status":"completed","finished_at":"2026-04-19T21:54:12.000000Z"}
```

No payload-level `step_id`.

### `message.user`

```json
{"text":"Fix issue 130"}
```

### `message.developer`

```json
{"message_type":"agents.md","source_path":"/abs/path/AGENTS.md","text":"..."}
```

Carries injected environment, AGENTS, skills, reviewer feedback, interruption notices, carryover prompts, and other developer-only replay-critical context.

### `message.assistant`

```json
{
  "phase":"commentary",
  "text":"I am auditing the persisted session file.",
  "tool_calls":[
    {
      "id":"call_123",
      "name":"shell",
      "input":{"command":"git status --short"},
      "presentation":{...}
    }
  ],
  "reasoning_items":[...]
}
```

This remains one event because assistant text, tool calls, and reasoning metadata currently share one ordering point.

### `tool.result`

```json
{"call_id":"call_123","tool_name":"shell","is_error":false,"output":{"exit_code":0,"output":"/tmp\n","truncated":false}}
```

This replaces both:

- `tool_completed`
- `message` with `role=tool`

V2 rule: tool results are persisted only once. No `message.tool` event exists in v2.

#### Hydrate Path For `tool.result`

This is the highest-risk migration boundary.

Provider-history hydration:

1. replay `message.assistant`
2. collect `tool_calls[].id`
3. replay `tool.result`
4. index each result by `call_id`
5. when building provider items, emit one synthetic `function_call_output` item for every assistant tool call that has a matching persisted `tool.result` and does not already have a materialized output in `history.replaced`

Transcript hydration:

1. replay `message.assistant`
2. render assistant text and tool-call rows from the assistant message itself
3. if a matching `tool.result` exists for a tool call and no materialized tool-result row already exists from compaction history, render one visible tool-result transcript row from that `tool.result`

State ownership rule:

- assistant tool-call intent comes only from `message.assistant.tool_calls`
- tool execution result comes only from `tool.result`
- visible tool result row is derived from `tool.result`
- provider `function_call_output` item is derived from `tool.result`
- `history.replaced.items` may materialize provider outputs directly after compaction; in that case replay must not synthesize duplicates

This keeps one persisted durable source for tool output while preserving both current replay consumers.

### `transcript.local`

```json
{"role":"compaction_notice","text":"Context compacted.","ongoing_text":"","visibility":"auto","diagnostic_key":""}
```

Used for persisted non-provider transcript rows such as compaction notices, cache warnings, and diagnostic entries.

### `history.replaced`

```json
{"engine":"","mode":"auto","items":[...]}
```

Minimal v2 keeps `items` as typed `[]llm.ResponseItem`. This is already restore-critical, rare, and not the dominant size contributor.

### `cache.request_observed`

```json
{"digest_version":1,"cache_key":"...","scope":"conversation","chunk_count":42,"terminal_hash":"..."}
```

### `cache.response_observed`

```json
{"digest_version":1,"cache_key":"...","scope":"conversation","chunk_count":42,"terminal_hash":"...","has_cached_input_tokens":true,"cached_input_tokens":128000}
```

### `cache.warning`

```json
{"cache_key":"...","scope":"conversation","reason":"reuse_dropped","lost_input_tokens":79000}
```

Runtime emits cache warnings only when provider usage reports a positive cached-input-token loss. Legacy restored warnings may omit `lost_input_tokens`.

## Kind Mapping

Current persisted v1 kinds mapped to v2 event types and current replay consumers:

| V1 kind | V2 type | Current consumers | Notes |
| --- | --- | --- | --- |
| `message` with `role=user` | `message.user` | `message_lifecycle.go`, `transcript_projector.go`, `persisted_event_entries.go`, `prompt_history.go`, `persisted_transcript_user_index.go` | replay-critical |
| `message` with `role=developer` | `message.developer` | same as above, plus interruption/headless/handoff/agents restore | replay-critical |
| `message` with `role=assistant` | `message.assistant` | same as above, plus tool-call presentation + handoff recovery | replay-critical |
| `message` with `role=tool` | dropped | currently read by transcript/provider replay helpers | replaced by `tool.result`; no direct v2 equivalent |
| `tool_completed` | `tool.result` | `chat_store.go`, `message_lifecycle.go`, `transcript_projector.go`, `persisted_event_entries.go`, `persisted_transcript_user_index.go` | single durable tool-output source in v2 |
| `local_entry` | `transcript.local` | `message_lifecycle.go`, `transcript_projector.go`, `persisted_event_entries.go`, `persisted_transcript_user_index.go` | replay-critical |
| `history_replaced` | `history.replaced` | `message_lifecycle.go`, `transcript_projector.go`, `persisted_event_entries.go`, `persisted_transcript_user_index.go` | replay-critical |
| `prompt_history` | `prompt_history.append` | `prompt_history.go` | replay-critical for picker/resume UX |
| `run_started` | `run.started` | `runs.go` projector, session view readers | persist in v2; remove payload `step_id` duplication |
| `run_finished` | `run.finished` | `runs.go` projector, session view readers | persist in v2; remove payload `step_id` duplication |
| `cache_request_observed` | `cache.request_observed` | `message_lifecycle.go`, `request_cache_lineage.go` restore | persist in v2 |
| `cache_response_observed` | `cache.response_observed` | same | persist in v2 |
| `cache_warning` | `cache.warning` | transcript-facing cache warning restore | persist in v2 |

## Production Write-Site Inventory

Only production write sites matter here. Tests intentionally excluded.

| Writer | Current persisted kind(s) | V2 classification | Reason |
| --- | --- | --- | --- |
| `Engine.RecordPromptHistory` in `server/runtime/engine_state.go` | `prompt_history` | persisted-in-v2 | still needed by `session.ReadPromptHistory()` |
| `Engine.appendMessage` in `server/runtime/engine_message_ops.go` | `message` | persisted-in-v2, but split by role into `message.user` / `message.developer` / `message.assistant` | current catch-all must become typed union |
| `Engine.appendMessageWithoutConversationUpdate` in `server/runtime/engine_message_ops.go` | `message` | persisted-in-v2, same split | same payload family, different runtime side effects only |
| `Engine.persistToolCompletion` in `server/runtime/engine_message_ops.go` | `tool_completed` | persisted-in-v2 as `tool.result` | single durable tool-output source |
| `Engine.appendPersistedLocalEntryRecord` in `server/runtime/engine_message_ops.go` | `local_entry` | persisted-in-v2 as `transcript.local` | non-provider transcript rows |
| `Engine.appendPersistedDiagnosticEntry` in `server/runtime/engine_message_ops.go` | `local_entry` | persisted-in-v2 as `transcript.local` | same family |
| `Engine.replaceHistory` in `server/runtime/compaction_utils.go` | `history_replaced` | persisted-in-v2 as `history.replaced` | compaction checkpoint source of truth |
| `Engine.observePromptCacheRequest` in `server/runtime/request_cache_lineage.go` | `cache_request_observed` | persisted-in-v2 as `cache.request_observed` | restore-critical for cache lineage tracker |
| `Engine.observePromptCacheResponse` in `server/runtime/request_cache_lineage.go` | `cache_warning`, `cache_response_observed` via `AppendTurnAtomic` | persisted-in-v2 as `cache.warning` and `cache.response_observed` | both restored today |
| `Store.AppendRunStarted` in `server/session/runs.go`, called from `server/runtime/exclusive_step.go` | `run_started` | persisted-in-v2 as `run.started` | run history remains file-backed |
| `Store.AppendRunFinished` in `server/session/runs.go`, called from `server/runtime/exclusive_step.go` | `run_finished` | persisted-in-v2 as `run.finished` | same |

Derived or dropped output:

- runtime `EventToolCallCompleted` / transcript-visible tool result row stay derived from `tool.result`
- runtime `EventRunStateChanged` stays ephemeral UI state; persisted source remains `run.started` / `run.finished`
- `message.role=tool` is dropped in v2; both provider replay and transcript replay derive from `tool.result`

## Why This Is Minimal

This proposal intentionally does not try to solve everything in one pass.

It does:

- eliminate the largest measured duplication family
- remove the opaque `payload` contract
- make all persisted kinds explicit Go structs
- keep replay logic close to today's semantics
- keep migration streamable line-by-line

It does not yet do:

- blob dedup for repeated AGENTS/environment/skills text
- delta compression
- binary attachments
- a fully normalized provider-history database
- splitting assistant tool calls into separate durable events

Those can follow later if the post-v2 size floor is still too large.

## Expected Size Win

On the audited sample, removing only `message.role=tool` should save about:

- `609,310` bytes from a `1,752,226` byte file
- about `34.9%` of whole-event bytes, not payload-only bytes

That estimate excludes smaller wins from:

- dropping duplicated `step_id` inside run payloads
- slightly tighter typed payloads versus ad hoc maps
- eliminating legacy replay fallback paths tied to `message.role=tool`

## Migration Shape

Minimal migration plan:

1. Add a dual reader that detects headered v2 files and still supports legacy v1 files.
2. Add typed v2 writer paths for all new sessions.
3. Add a streaming v1 -> v2 rewrite migrator for existing files.
4. During migration, collapse this v1 pair:
   - `message` with `role=tool`
   - matching `tool_completed`
   into one `tool.result` event.
5. Keep all other replay-critical data semantically identical in the first pass.

Important migration rule:

- v1 -> v2 rewrite must be streaming and must not load the full file into memory.

## Implementation Notes

Recommended Go model:

- `EventFileHeaderV2`
- `EventEnvelopeV2`
- `EventType` enum
- one payload struct per event type
- one custom decode switch on `type`

Recommended immediate cleanup after v2 lands:

- delete restore paths that special-case `message.role=tool`
- make `tool.result` the only durable tool completion source
- remove prompt-history legacy fallback from user `message` events for newly-written v2 files
