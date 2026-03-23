# Decisions

## Product Scope

- Build a minimal terminal coding agent focused on output quality, speed, and professional workflows.
- Tech stack: Go + Bubble Tea; no TypeScript.
- Public docs site uses Astro + Starlight from the repository `docs/` directory, deploys as a fully static GitHub Pages site, and mirrors the root `README.md` as the initial docs home.
- v1 excludes MCP, plugins, and native subagent orchestration.
- Skills are supported via AGENTS-driven `SKILL.md` discovery/injection from `~/.builder/skills` and `<workspace>/.builder/skills`.
- Full-access execution in v1 (no sandbox).
- Architecture must remain pluggable/composable with low-friction extension points.
- Working name is `builder` and must stay easy to rename.

## Core Runtime And Tools

- Core tools: `shell`, `view_image`, `patch`, `ask_question`.
- Compatibility wrapper tool `multi_tool_use_parallel` is supported (Codex-style schema) and executes referenced `functions.*` tools concurrently while returning results in declared order.
- One app instance runs one active conversation.
- Tool execution concurrency inside a model step is unbounded.
- Parallel call results are always returned in model-declared order.
- If one parallel call fails, in-flight calls are allowed to finish before returning ordered results.
- Ordered-result buffering is strict and uncapped in v1.

## Shell Tool

- Runs in the user login shell.
- Stateless per call (no persistent shell process state between calls).
- Executes in non-TTY mode (pipes, not PTY).
- Uses direct shell invocation only (no runtime command parsing/AST preprocessing).
- Inherits parent environment and adds non-interactive hints.
- Merges stdout/stderr into one stream without origin tags.
- Default timeout is 5 minutes.
- Per-call timeout override is allowed up to 1 hour.
- Non-zero exit is recoverable (does not auto-abort the turn).
- No automatic retry for shell process-launch failures.
- Interrupt escalation is `SIGINT` then `SIGKILL` after 10s grace.
- Background shell processes (`exec_command` / `write_stdin`) are app-global, not session-scoped.
- Background process ids are app-global within one app instance; owner session metadata is advisory for routing notices/history, not an access-control boundary.
- `/ps` may surface and operate on background processes started from other sessions in the same app instance; this is intentional in v1 to preserve operator visibility/control of long-running jobs.

## Patch Tool

- Apply is atomic: malformed/conflicting patch means no file changes.
- Allowed operations in v1: add/update/move/delete.
- `Delete File` participates in the same atomic patch transaction as add/update/move.
- Patch targets are validated with real-path resolution.
- No timeout and no automatic retries.
- Patch success persistence includes patch input + apply-result metadata.
- Outside-workspace edits are approval-gated unless explicitly enabled.
- `allow_non_cwd_edits=false` by default.
- If outside-workspace approval is denied, return an explicit non-circumvention tool error instructing manual user edits when essential.

## View Image Tool

- `view_image` path resolution uses absolute + canonical real paths before access checks.
- Workspace boundary checks apply after symlink resolution; symlink escapes outside workspace are blocked by default.
- Paths containing `..` are evaluated via canonical resolution; they are only allowed when the canonical target remains within the workspace boundary.
- Outside-workspace file reads are approval-gated via the same approver contract as `patch`, with per-call/per-session allow semantics.
- Approved outside-workspace reads are written to run logs with requested/resolved path metadata for auditability.

## Tool Output, Retries, And Failure Handling

- Large tool output is truncated for model consumption using standardized payloads (head+tail + truncation metadata, threshold configurable).
- Model-step transient failures use exponential backoff retries with 5 attempts (`1s, 2s, 4s, 8s, 16s`).
- Model/API errors in `ongoing` are shown as concise single-line errors; full details remain in detail view/logs.

## Ask Question

- `ask_question` is shared by model and runtime, with unified UI.
- Runtime `ask_question` pauses active pipeline until answered.
- Waits indefinitely (no timeout/default cancel).
- Model-callable `ask_question` is limited to ordinary question/suggestion/freeform asks. Approval prompts are internal automated workflows only and are not exposed to the model tool schema.
- Supports suggestions + freeform override.
- With suggestions: option picker includes a dedicated `Something else` branch, and `Tab` toggles between picker and freeform editing.
- Suggestion asks use a schema-level `recommended_option_index` (1-based) instead of embedding `Recommended:` into suggestion text. Runtime validation requires that field whenever suggestions are present, but freeform-only asks omit both suggestions and the recommended index.
- The recommended suggestion is highlighted in green in the ask UI and transcript/detail suggestion rendering without adding textual labels.
- Selecting `Something else` with empty input opens freeform editing; submitting from that path still requires non-empty commentary.
- For suggestion asks, returning to picker keeps any pending freeform draft visible as muted text and submits/restores that draft when the user picks an option or tabs back into editing.
- For internal approval asks, the picker only shows the fixed built-in options `Allow once`, `Allow for this session`, and `Deny`; `Tab` adds commentary for the currently selected option and that commentary is injected through the regular queued user-message steering flow. Allowing continues transparently to the model. Denial fails the original guarded tool call with an authoritative rejection error instead of surfacing a separate approval answer event.
- Freeform ask input uses the same prompt-box editing/cursor behavior as the main input.
- Without suggestions: freeform directly.
- Source origin is not labeled in UI.
- Answers are persisted as explicit summary text (including selected option number and any additional freeform commentary).
- Queue semantics are strict FIFO, in-memory only, and submitted answers are not editable.
- Optional post-answer action binding is supported.
- Action handling uses typed registry (stable id + payload schema + handler).
- v1 ships registry scaffolding only (no built-in actions).
- Action payload schemas are unversioned in v1.
- Unknown action id is fatal (crash in all build modes).

## Sessions, Persistence, And Durability

- Sessions support stop/resume.
- Persistence root is configurable; default `~/.builder`.
- Storage layout is workspace-scoped containers (`<workspace-folder-name>-<random-uuid>`) with UUID session directories.
- Session persistence format: split `session.json` + `events.jsonl`.
- `events.jsonl` is append-only on normal writes; periodic compaction rewrites canonical JSONL to control long-session growth.
- Session directory names are UUID-only.
- Session start/setup becomes immutable at first model request dispatch.
- Resumed sessions keep locked setup immutable, except thinking level.
- Lock covers model + core generation params, enabled tools, tool schema/description snapshot, and system prompt snapshot; thinking level is mutable mid-session.
- Transcript message order is immutable for cache stability.
- Canonical model context/history is stored as Responses API input items; message-only chat is UI projection.
- Tool-call identity prefers provider-native ids; UUID fallback when missing.
- Retry collisions on tool-call ids overwrite prior-attempt ids.
- Event identity uses monotonic sequence id + wall timestamp.
- No event integrity hash chain.
- Durability strategy: async capture with append-only turn writes and configurable fsync policy.
- Tool results persist at tool-completion boundary.
- History replacement during compaction persists as atomic `history_replaced` events.
- Crash-loss tolerance allows losing up to one in-flight tool call.
- No session event compression.

## Interrupts, Queueing, And In-Turn Messaging

- In-turn user messaging supports both mid-run injection and queued post-turn send.
- Queue/send hotkey is `Tab`; compatibility alias: `Ctrl+Enter`.
- Known `Ctrl+Enter` CSI encodings normalize to the same queue action.
- Mid-run injection is soft-insert only (delivered at safe boundary after current tool completion; no forced interruption).
- Pending user message order is strict FIFO.
- Pending queue is unbounded.
- Queued hotkey messages are in-memory only.
- Injected mid-run messages persist only on delivery boundary.
- `Ctrl+C` interrupt is turn-local (stop current model step + active tool process, keep app/session alive).
- Interrupt injects developer-role control message: `User interrupted you`.
- Post-interrupt state returns to idle with input ready.
- Resume after interrupt requires explicit user text (no autogenerated resume message).
- Crash recovery is bifurcated:
- Mid-step crash resumes via interrupt flow.
- Otherwise restore normal state directly.

## Prompts, Tool Schemas, And Instruction Sources

- Prompt sources live in repository files.
- System prompt is a markdown file in-repo.
- Tool definitions (names, descriptions, schemas) are centralized in one file.
- Tool definitions are also the single source of truth for tool runtime availability, request exposure/gating (including multimodal and native-web-search opt-in), hosted-output decoding, transcript metadata, and render hints.
- Prompts/tool definitions are build-embedded (runtime-hardcoded from source files; no runtime file loading dependency).
- Instruction precedence follows provider/API role semantics (no custom override layer).

## AGENTS.md Injection

- AGENTS injection happens once per session and is not repeated on resume.
- Injection order on first user turn is deterministic:
- Existing restored messages.
- Global `~/.builder/AGENTS.md` as `developer` message when present.
- Workspace-root `AGENTS.md` as `developer` message when present.
- Current user prompt.
- Injection uses structured fenced formatting including source path.

## Auth And Credential Policy

- OpenAI auth supports API key and subscription OAuth.
- Auth is global app-level (not per-session).
- Valid auth is required before startup completes.
- Startup auth failure uses blocking error screen with retry.
- Startup auth menu exposes exactly three OAuth methods:
- `oauth_browser`, `oauth_browser_paste`, `oauth_device`.
- OAuth failure does not auto-fallback to API key.
- OAuth tokens auto-refresh silently; only refresh failures are surfaced.
- Global auth method can be switched only while idle.
- Auth credentials are stored in plain JSON under the persistence root (`auth.json`) with restrictive file permissions; no OS secure-store backend exists in v1.

## Configuration

- User settings are loaded from `~/.builder/config.toml` with first-run auto-bootstrap.
- Unknown `config.toml` keys are rejected as configuration errors.
- Configuration precedence: `CLI overrides > environment > settings file > built-in defaults`.
- Thinking level passes OpenAI values through unchanged (including `xhigh`) and applies only to OpenAI model families.
- Context window is explicit setting: `model_context_window` (default `400000`).
- Validation requires `context_compaction_threshold_tokens < model_context_window`.
- Responses API `store` is configurable via `store` / `BUILDER_STORE`, default `false`.
- Compaction routing is configurable by `compaction_mode` (`native|local|none`, default `native`).
- Terminal notification backend is configurable by `notification_method` (`auto|osc9|bel`, default `auto`).
- TUI alternate-screen policy is configurable by `tui_alternate_screen` (`auto|always|never`, default `auto`).
- `tools.web_search` is enabled by default; `web_search` controls whether provider-native web search is activated (`native`) or disabled (`off`).
- `tools.view_image` is enabled by default; runtime only advertises it to models that support multimodal inputs.

## Context Management And Compaction

- Auto-compaction is enabled near context limits.
- Builder may compact before sending the next user prompt when current context usage is already within a configurable lead band of the normal compaction threshold; in that case the prompt is queued, compaction runs first, and the queued prompt is submitted immediately after compaction completes.
- Pre-submit compaction lead uses `threshold - min(model_context_window - threshold, pre_submit_compaction_lead_tokens)`, with `pre_submit_compaction_lead_tokens` defaulting to `15000`.
- Auto-compaction failure aborts the current turn.
- `compaction_mode=none` disables manual and automatic compaction.
- Manual compaction is available via `/compact` while idle; optional arguments are appended as compaction guidance.
- Successful manual `/compact` appends a hidden developer carryover message containing the last visible user prompt so the post-compaction model context still knows what the user most recently asked for.
- Local compaction instructions are injected as final `developer` message.
- Local compaction summary generation reads full provider history from latest compaction checkpoint onward (or from start if none).
- Local compaction summary generation keeps tool declarations for request shape/cache stability but runtime rejects any returned tool calls.
- Manual compaction failures are surfaced to UI without terminating session.
- Native compaction eligibility is capability-driven and user-configurable.
- `type=compaction` items and encrypted reasoning/compaction payloads are treated as opaque and replayed unchanged.
- Compaction lifecycle emits and persists started/completed/failed events.
- UI shows one compacted notice line per successful compaction; ongoing suppresses detailed summary content; detail shows full local summary when available.

## Model Defaults

- Model seed is unset by default.
- Temperature is fixed to `1`.
- Max output tokens are unlimited by default.

## UI, Modes, And Rendering

- UI has two modes: `ongoing` (default) and `detail`, toggled by `Shift+Tab` or `Ctrl+T`.
- `ongoing` remains minimal:
- Show command start and file hint previews with truncation.
- If collapsing is not possible, show first command line and ellipsize.
- Hide thinking traces, preambles, outputs, and diffs.
- Ongoing preview sizing is fixed: command max `80`, file max `60`, soft-wrap allowed.
- Ongoing line prefix is `> `.
- Shell command previews remain syntax-highlighted in both modes; ongoing renders them with lower-contrast `preview` styling plus terminal `faint`, while detail keeps full syntax colors.
- Assistant text streams in ongoing mode.
- Tool output is not streamed live; show running status and reveal on completion.
- `detail` is a non-streaming snapshot view.
- Mid-step entry shows latest completed snapshot only.
- Snapshot is static while open (no live refresh indicator/action).
- Snapshot scope is full session transcript up to latest completed step.
- Detail transcript rendering is flat continuous stream (no grouped sections).
- Step-end markers appear in detail only.
- Switching detail -> ongoing restores prior ongoing scroll position.
- Mode-toggle events are UI-ephemeral and not persisted.
- Detail is a fullscreen pager-style transcript overlay (input/queued/picker hidden).
- Ongoing mode uses native terminal scrollback by replaying committed transcript history into the normal buffer and appending only new committed transcript deltas.
- Main UI startup stays in the normal buffer even when `tui_alternate_screen=always`, because ongoing-mode replay must remain visible in terminal scrollback.
- Main UI startup clears the visible terminal viewport once before rendering (including `native` mode), so each session (including `/new`) starts from a clean visible slate.
- After startup, ongoing-mode normal-buffer history is append-only. Once a transcript line is emitted into scrollback, it is immutable: no retroactive restyling, no in-place rewrites, no clear-and-replay, and no full-buffer re-emission to reflect later state.
- Non-append transcript mutations (compaction/rollback-style rewrites) rebase the internal formatter state without re-emitting prior history, to avoid duplicate scrollback output.
- Assistant streaming is rendered in the ongoing live viewport and is not appended to normal-buffer scrollback until commit.
- Pending tool-call activity in ongoing mode lives only in the volatile live region, not in committed normal-buffer scrollback.
- Ongoing-mode glyphs reserve `@` for web search tool calls; reviewer status/suggestion entries use `§`.
- Pending tool-call previews in the live region use the same rendering/layout as normal committed `tool_call` previews, with no pending-only labels, keywords, or extra markers.
- Tool completion in ongoing mode appends exactly one final committed line for that tool, already rendered in its terminal state. Ongoing mode must never recolor or otherwise mutate an earlier emitted tool line.
- Parallel tool calls in ongoing mode commit through a stable frontier: later completed calls remain in the live region until all earlier pending calls are ready, but they render in their final tool state immediately; only still-running calls show the spinner. Newly committable final lines append once in transcript order.
- In ongoing main-input mode, `Up`/`Down` are reserved for prompt-history recall at whole-buffer boundaries and for normal multiline cursor movement otherwise; they do not scroll the ongoing transcript.
- Ongoing transcript scrolling remains on `PgUp`/`PgDn`; failed prompt-history navigation attempts emit a plain terminal BEL with no transient UI notification.
- Rationale: terminal normal-buffer scrollback cannot be safely rewritten portably; committed replay is the single source of truth for persistent formatted history.
- Ongoing mode keeps mouse capture disabled by default to preserve native text selection behavior.
- Ongoing mode never enables terminal alternate-scroll (`?1007`).
- Detail transcript overlay uses terminal alt-screen (`?1049`) when `tui_alternate_screen != never`.
- While detail overlay is active, terminal alternate-scroll (`?1007`) is enabled to support wheel-driven transcript navigation; it is disabled again on leaving detail.
- Mouse capture remains disabled, so text selection/copy in detail overlay stays terminal-native.
- Rationale: ongoing must preserve long-lived normal-buffer scrollback and smooth native selection/copy; detail is an inspection surface where wheel navigation is prioritized without taking over mouse capture.
- No timestamps are shown in UI.
- Streaming paint cadence is 16ms with token coalescing per flush tick.
- Main status line is compact and fixed: activity indicator, mode, model label, cache section, transient warning; context meter is right-aligned.
- Model label appends thinking level when reasoning effort is supported by the resolved model contract; unknown non-empty model ids default to reasoning-capable.
- Status line includes right-aligned context meter (10-char bar + `% ctx window`, green/yellow/red at `<50%`, `50-<80%`, `>=80%`).

## Startup And Session Selection UX

- Startup shows recent sessions with pick-or-new flow.
- Startup session list is scrollable with no cap.
- If no sessions exist, startup goes directly to new-session setup.

## Slash Commands

- Leading slash input enters command mode when first non-space char is `/`.
- Picker matches only first token and updates continuously.
- After whitespace, command enters argument mode and picker hides.
- `Enter` runs the currently selected slash command, including the default first match for partial input.
- `Tab` on a partial selected slash command autocompletes it and inserts a trailing space for arguments.
- Unknown slash commands are sent to model as normal user prompts.
- Built-in commands: `/logout`, `/exit`, `/new`, `/resume`, `/compact`, `/name`, `/thinking`, `/fast`, `/review`, `/init`, `/supervisor`, `/autocompaction`, `/ps`, `/back`.
- Exact known slash commands use the normal queued-input drain path when queued, including conditionally fresh-session commands like `/review` and `/init`; they are never sent to the model as plain user prompts.
- Run-safe commands execute immediately while busy.
- Non-run-safe known commands while busy are rejected with transient status-line error.
- `/review` auto-submits the embedded review rubric prompt; it stays in-place for empty sessions and forks a fresh child session once the current session already has a visible user prompt. Optional args are appended as review scope.
- `/supervisor` controls runtime reviewer invocation for the current session only.
- `/supervisor` toggles when called without args; `/supervisor on|off` sets explicitly.
- `/supervisor` emits user-visible confirmation in transcript + status line and does not persist to config.
- `/autocompaction` controls runtime auto-compaction invocation for the current session only.
- `/autocompaction` toggles when called without args; `/autocompaction on|off` sets explicitly.
- `/autocompaction` emits user-visible confirmation in transcript + status line and does not persist to config.
- Built-in prompt commands use embedded markdown templates.
- Slash commands support file-backed prompts from:
- `./.builder/prompts`, `./.builder/commands`, `~/.builder/prompts`, `~/.builder/commands`.
- Non-recursive `.md` scan, merged namespace, precedence: local > global and `prompts` > `commands`.
- File command id format: `prompt:<filename-without-extension>`.
- Triggering file command submits file content verbatim as `user` message.

## Notifications

- Ring terminal bell when a new `ask_question` is shown.
- Ring on turn end only if the turn executed at least two tool calls.
- Turn-end ringing is keyed by runtime step id and `tool_call_started`/`assistant_message` events.
- Turn-end notification text includes assistant response preview when available, else `<session title>: turn complete` with `builder` as the fallback title.
- Ask notifications include the ask text as `<session title>: Question: <question>` or `<session title>: Action required: <question>`.
- `auto` notification method prefers OSC 9 on supported terminals and falls back to BEL.
- OSC 9 notifications still emit a separate BEL so supported terminals get both notification and audible bell.
- OSC 9 is disabled when `WT_SESSION` is set.

## API Headers And Provider Wiring

- OpenAI requests always set `originator` and `User-Agent` headers.
- `session_id` header is sent whenever a session id exists, for both OAuth and API key auth.
- LLM provider wiring uses a provider-factory seam so runtime/app constructs `llm.Client` via provider selection (default OpenAI), enabling provider expansion without runtime refactors.

## Headless Mode

- `builder run "prompt"` is the supported headless subagent interface.
- Executes a single non-interactive prompt with existing runtime/session persistence.
- Creates/resumes normal sessions and auto-names unnamed sessions `<session-id> subagent`.
- Default timeout is infinite; `--timeout` can bound execution.
- Output modes are explicit: default `--output-mode=final-text`, optional `--output-mode=json`.
- JSON mode emits exactly one final object on `stdout`: `status`, `result`/`error`, `session_id`, `session_name`, `duration_ms`, plus continuation metadata when available.
- Final-text mode emits the final assistant text to `stdout`, optionally followed by a continue hint.
- Progress is quiet by default and is emitted to `stderr` only when `--progress-mode=stderr`.

## Experimental Reviewer

- Post-turn reviewer agent exists behind config and defaults to `reviewer.frequency = "edits"`.
- Reviewer runs only after completed assistant final handoff and only if the completed turn executed at least one tool call.
- Reviewer uses more aggressive tool-output truncation than main-agent path.
- Reviewer contract is minimal JSON: `{"suggestions":["..."]}`; invalid payloads are ignored non-fatally.
- If suggestions exist, runtime appends them as `developer` message and runs one extra main-agent follow-up pass.
- Follow-up noop token is exact `NO_OP`; if emitted, runtime keeps original assistant final answer.
- Reviewer pass is single-shot (no recursive review of review).
