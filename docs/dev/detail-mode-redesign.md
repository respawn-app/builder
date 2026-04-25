# Detail Mode Redesign

## Goal

Detail mode becomes a fast transcript inspector, not a raw transcript dump. The default view is compact and navigable by message. Full content stays available by expanding individual messages.

Ongoing remains the native-scroll, append-only transcript surface. Detail is an alt-screen inspection surface, but it must still preserve native text selection. Mouse capture must not be enabled for ongoing or detail; detail may enable terminal alternate-scroll while active for wheel-friendly navigation on terminals that support it.

The shared app status line is intentionally compact across ongoing and detail. Its left side starts with the activity indicator followed by one plain space, then branch/model/process/server metadata separated with ` · `. The dot separator must not appear immediately after the activity indicator.

Success metrics:

- Opening detail on a transcript with long command/file outputs shows mostly one row per message/tool, not hundreds of output lines.
- A 500-entry transcript can be scanned by repeated `Up`/`Down` without line-by-line scrolling through raw output.
- Expanding a long tool call reveals the same full content available in old detail mode.
- Collapsed detail first render should be proportional to number of visible/loaded entries and collapsed preview size, not total output line count.
- Full rendering cost for long outputs is paid only when expanded or needed for viewport metrics.

## Locked Product Decisions

- Legacy sessions with missing metadata use degraded generic labels. Builder must not classify old reminders by parsing prompt/reminder text.
- Unknown or malformed entries with recoverable text are visible in ongoing and detail. Empty unknown/malformed entries are detail-only diagnostics.
- Multiple messages may be expanded at once.
- Tool calls with error results do not auto-expand; collapsed detail shows compact input plus structured error summary when runtime/projection provides one.
- Detail does not use dedicated collapsed/expanded glyphs. Multi-line detail items use tree-style continuation guides after the first rendered line: `│` for middle lines and `└` for the last line.
- Selection never changes foreground colors. Selection background/fill has the lowest background priority.

## Interaction Model

- `Up` scrolls one rendered line up.
- `Down` scrolls one rendered line down.
- `Enter` toggles the selected message between collapsed and expanded.
- `PgUp`/`PgDn` keep page scrolling detail content.
- Mouse wheel keeps scrolling detail content when the terminal can deliver wheel navigation without mouse capture.
- `Tab` or the existing mode toggle returns to ongoing.

Selection is message-oriented, not line-oriented. Scrolling is line-oriented. `Up`/`Down` move the viewport by one rendered line when scrolling is possible. After line, page, wheel, or alternate-scroll movement, compact detail selects the selectable message nearest the vertical center of the viewport. If the center line is inside a tall expanded entry, that entry stays selected while its body scrolls through the center. At top/bottom bounds, selection falls back to the nearest visible selectable entry to that center anchor. If expansion makes the selected message leave the viewport, detail scrolls just enough to reveal it. Selection state is UI-ephemeral and is not persisted.

Detail rows do not show a dedicated collapsed/expanded glyph. The first rendered line keeps the normal role/tool symbol. When an item renders more than one line, continuation lines replace the role-prefix column with a faint tree guide: `│` for middle lines and `└` for the last rendered line.

The selected message uses a selection background/fill across every rendered line of that message and across the full terminal width. Compact detail also renders highlighted blank spacer rows with the selected side rail immediately above and below the selected message when adjacent viewport rows are available; these spacers are visual-only viewport decoration and do not participate in scroll metrics or transcript line counts. When the selected item can expand, its leading role symbol is replaced with `▶` while collapsed and `▼` while expanded; unselected items keep their normal role symbols. The status line mirrors this affordance with `Enter to expand` or `Enter to collapse`. Selection must not change foreground colors. Selection background is lowest priority: any semantic background already present on a cell, such as patch diff backgrounds or syntax-highlight backgrounds, wins over selection background.

## Three Axes

Keep these concerns separate in code and tests:

- Visibility decides whether an entry exists in ongoing, detail, both, or neither. Visibility is owned by runtime projection and the visibility matrix, not by collapsed-label logic.
- Collapsed label decides what detail shows while the item is collapsed. It must be compact, deterministic, and metadata-first.
- Expanded content decides what detail reveals after `Enter`. It must preserve full stored content and tool output, even when collapsed labels are degraded for legacy data.

Unknown/malformed policy in one sentence: unknown or malformed entries with non-empty text are visible in ongoing and detail, empty unknown/malformed entries are detail-only diagnostics, malformed metadata on known entries keeps that entry's existing visibility when safer than promotion, and every fallback emits one `detail_classifier` warning diagnostic with role/message_type/tool facts.

Visibility source of truth:

- Known roles/message types use the existing O/OC/D/X visibility matrix from `docs/dev/decisions.md`.
- Unknown or malformed entries with recoverable text are an explicit matrix extension: treat as `O` for operator visibility.
- Empty unknown or malformed entries are `D` diagnostics.
- Collapsed-label logic must not independently hide entries.

## Rendering Contract

Every detail item has:

- Stable transcript entry range.
- Collapsed renderer.
- Expanded renderer.
- Stable collapsed label that does not require parsing styled output.
- Structured key suitable for selection/expansion state.

Collapsed rows should usually fit in one rendered line. User and assistant text are the main exception: they show up to three rendered lines to preserve enough context for scanning. Tool calls with error results may show two collapsed lines: compact tool input plus structured error summary. Collapsed content should use the same role symbols and semantic colors as current detail rendering.

Expansion renders the same full content detail mode renders today, unless this spec says otherwise. Tool calls expand to show the full tool input and full tool output/result. If a tool call has no matching result yet, expansion shows full input and no synthetic empty output.

Detail state is per message item, not per rendered line. Lines are a viewport projection of message items. Selection, expansion state, and viewport anchors reference item identity/ranges, then render into lines after collapse/expand decisions.

Detail uses the same role-group separator policy as ongoing/native transcript rendering, but renders group breaks as blank lines rather than divider rules. Consecutive tool rows form dense chunks; transitions between role groups get one blank line. Detail relies on role symbols, tree-style continuation guides, selection, and vertical rhythm to reduce chrome while preserving scanability.

Performance rules:

- Collapsed mode must not pre-render expanded content for every item.
- Detail item construction may inspect metadata and short text previews, but must not syntax-highlight full patches/shell output until expanded or visible.
- Viewport metrics should cache collapsed and expanded line counts per item key and invalidate by width, expansion state, and transcript revision.
- Paging must continue to avoid loading full unbounded `events.jsonl` into memory.
- Benchmark detail first-open, one-step item navigation, page scroll, expand long tool, collapse long tool, and resize with at least 600 mixed entries.

## Authoritative Rendering Matrix

The appendix tables below are the authoritative implementation matrix for visibility, collapsed labels, and expanded content. Do not duplicate row-level policy elsewhere. Product prose may summarize behavior, but classifier tests must be generated from or mirrored against the appendix rows.

Fallback rules:

- If a role has `OngoingText`, collapsed detail uses that before deriving any text preview. This covers background notices and any future server-owned compact rows.
- If a tool call has `ToolCallMeta.CompactText`, collapsed detail uses the same compacting path as ongoing. Patch calls prefer `PatchSummary`; shell calls prefer first non-empty command line plus ellipsis when multiline.
- If a non-error text preview is needed, use first non-empty rendered line for one-line roles, or first 3 rendered lines for user/assistant roles. Empty content becomes the role label, e.g. `System notice`, `Reviewer suggestions`, `Tool output`.
- Error roles never collapse by line count. Tool calls with error results stay collapsed by default, but collapsed detail shows compact input plus a structured error summary only when runtime/projection provides one; expanding reveals full input/output.
- Unknown developer/context messages use `ChatEntry.MessageType` if present for future labels; otherwise first non-empty line; otherwise `Developer context`.

## Current Type Appendix

This appendix is the implementation coverage target. Classifier tests should mirror these tables. Each row has a status and source-of-truth hook so coverage can be executable, not just descriptive.

Statuses:

- `current-emitted`: emitted by current runtime/projection/rendering code.
- `legacy-fallback`: expected only from old/partial persisted data or orphaned rows.
- `future-only`: policy for unknown/future data.

Source hooks:

- `visibility`: `server/runtime/transcript_message_visibility.go`
- `meta`: `server/runtime/meta_context.go`
- `events`: `server/runtime/transcript_event_entries.go`
- `tools`: `server/runtime/tool_presentation.go`
- `render`: `cli/tui/model_rendering.go` and `cli/tui/model_rendering_style.go`
- `styles`: `cli/tui/message_styles.go`
- `classifier`: the new detail-entry classifier introduced by this spec

### Source Message Types

| Status | Source hook | Source `llm.MessageType` | Source role(s) | Transcript role | Visibility | Collapsed label | Expanded content |
| --- | --- | --- | --- | --- | --- | --- | --- |
| current-emitted | visibility/events | empty/default | `user` | `user` | Ongoing + detail | First 3 rendered lines. | Full user text. |
| current-emitted | events/render | empty/default | `assistant` | `assistant` or `assistant_commentary` by phase | Ongoing + detail | First 3 rendered lines. | Full assistant text. |
| current-emitted | events/render | empty/default | `tool` | `tool_result_ok` or `tool_result_error` | Detail unless paired into tool-call block | Orphan result first non-empty line. | Full result text. |
| current-emitted | meta/visibility | `agents.md` | `developer` | `developer_context` | Detail-only | `<source_path> file content`; legacy fallback if no path. | Full AGENTS content. |
| current-emitted | meta/visibility | `skills` | `developer` | `developer_context` | Detail-only | `Skill guidance on N skills`; if count unknown, `Skill guidance`. | Full skills content. |
| current-emitted | meta/visibility | `environment` | `developer` | `developer_context` | Detail-only | `Environment info`. | Full environment content. |
| current-emitted | meta/visibility | `headless_mode` | `developer` | `developer_context` | Detail-only | `Headless mode instructions`. | Full headless prompt. |
| current-emitted | meta/visibility | `headless_mode_exit` | `developer` | `developer_context` | Detail-only | `Interactive mode restored`. | Full exit prompt. |
| current-emitted | meta/visibility | `worktree_mode` | `developer` | `developer_context` | Detail-only | `Switched to worktree <name>`; if name unknown, `Switched worktree`. | Full worktree prompt. |
| current-emitted | meta/visibility | `worktree_mode_exit` | `developer` | `developer_context` | Detail-only | `Returned from worktree <name>`; if name unknown, `Returned from worktree`. | Full worktree-exit prompt. |
| current-emitted | visibility/events | `compaction_summary` | `developer` or `user` | `compaction_summary` | Detail-only | `Context compacted for the Nth time`; if count unknown, `Context compacted`. | Full compaction summary. |
| current-emitted | visibility | `interruption` | `developer` | `interruption` | Ongoing + detail | `You interrupted`. | Same text. |
| current-emitted | visibility | `error_feedback` | `developer` | `developer_feedback` | Ongoing + detail | First non-empty line. | Full feedback text. |
| current-emitted | visibility | `compaction_soon_reminder` | `developer` | `warning` | Detail-only by default role visibility | `Context fillup reminder notice`. | Full warning text. |
| current-emitted | visibility | `handoff_future_message` | `developer` | `developer_context` | Detail-only | `Future-agent context`. | Full future-agent message. |
| current-emitted | visibility | `reviewer_feedback` | `developer` | no raw transcript entry | Hidden raw artifact | N/A. | N/A; reviewer UI uses reviewer transcript roles. |
| current-emitted | visibility/events | `background_notice` | `developer` | `system` | Ongoing + detail, ongoing uses compact content | Compact content, else first system line. | Full background notice text. |
| current-emitted | visibility | `manual_compaction_carryover` | `developer` | `manual_compaction_carryover` | Detail-only | `Last user message preserved for compaction`. | Full carryover content. |
| current-emitted | events | `custom_tool_call_output` | `tool` | `tool_result_ok` or `tool_result_error` | Detail unless paired into tool-call block | Orphan result first non-empty line. | Full custom tool output. |
| future-only | visibility/classifier | unknown | `developer` | `developer_context` | Ongoing + detail when text exists; otherwise detail-only diagnostic | `Developer context: <message_type>`. | Full content. |
| future-only | visibility/classifier | unknown | `user` | `user` | Ongoing + detail when text exists; otherwise detail-only diagnostic | First 3 rendered lines. | Full content. |
| future-only | visibility/classifier | unknown | `assistant` | `assistant` or `assistant_commentary` by phase | Ongoing + detail when text exists; otherwise detail-only diagnostic | First 3 rendered lines. | Full content. |
| future-only | visibility/classifier | unknown | `tool` | `tool_result_ok` or `tool_result_error` | Ongoing + detail when text exists; otherwise detail-only diagnostic | Orphan result first non-empty line. | Full output. |

### Transcript And Render Roles

| Status | Source hook | Transcript/render role | Source | Visibility | Collapsed label | Expanded content |
| --- | --- | --- | --- | --- | --- | --- |
| current-emitted | visibility/events/render | `user` | User message | Ongoing + detail | First 3 rendered lines. | Full user text. |
| current-emitted | events/render | `assistant` | Assistant final/default phase | Ongoing + detail | First 3 rendered lines. | Full assistant text. |
| current-emitted | render | `assistant_commentary` | Assistant commentary phase | Ongoing + detail | First 3 rendered lines. | Full commentary text. |
| current-emitted | visibility/render | `thinking` | Reasoning summary | Detail-only | `Reasoning summary` plus first useful line. | Full reasoning text. |
| current-emitted | visibility/render | `thinking_trace` | Reasoning trace | Detail-only | `Reasoning summary` plus first useful line. | Full reasoning text. |
| current-emitted | visibility/render | `reasoning` | Reasoning summary/stream | Detail-only | `Reasoning summary` plus first useful line. | Full reasoning text. |
| current-emitted | events/render/tools | `tool_call` | Raw persisted tool call | Ongoing compact + detail | Same compact input as ongoing. | Full input plus matched output. |
| current-emitted | render/tools | `tool` | Default tool call, no result yet | Ongoing compact + detail | Same compact input as ongoing. | Full input. |
| current-emitted | render/tools | `tool_success` | Default tool call with success result | Ongoing compact + detail | Same compact input as ongoing, success style. | Full input plus full output unless omitted by metadata. |
| current-emitted | render/tools | `tool_error` | Default tool call with error result | Ongoing compact + detail | Same compact input as ongoing plus structured error summary when available, error style. | Full input plus full error output. |
| current-emitted | render/tools | `tool_shell` | Shell tool, no result yet | Ongoing compact + detail | First command line, same as ongoing. | Full command input. |
| current-emitted | render/tools | `tool_shell_success` | Shell tool success | Ongoing compact + detail | First command line, success style. | Full command input plus full output. |
| current-emitted | render/tools | `tool_shell_error` | Shell tool error | Ongoing compact + detail | First command line plus structured error summary when available, error style. | Full command input plus full error output. |
| current-emitted | render/tools | `tool_patch` | Patch/edit tool pending | Ongoing compact + detail | `⇄` plus patch summary. | Full patch input. |
| current-emitted | render/tools | `tool_patch_success` | Patch/edit tool success | Ongoing compact + detail | `⇄` plus patch summary, success style. | Full rendered patch/diff; successful empty result omitted. |
| current-emitted | render/tools | `tool_patch_error` | Patch/edit tool error | Ongoing compact + detail | `⇄` plus patch summary and structured error summary when available, error style. | Full rendered patch/diff plus full error output. |
| current-emitted | render/tools | `tool_question` | Ask-question tool success/pending | Ongoing + detail | Question only. | Question, options, recommendation, answer if present. |
| current-emitted | render/tools | `tool_question_error` | Ask-question tool error | Ongoing + detail | Question plus structured error summary when available, error style. | Question, options, recommendation, error/answer. |
| current-emitted | render/tools | `tool_web_search` | Web-search tool pending | Ongoing compact + detail | Compact query/input. | Full web-search input. |
| current-emitted | render/tools | `tool_web_search_success` | Web-search tool success | Ongoing compact + detail | Compact query/input, success style. | Full input plus output/result. |
| current-emitted | render/tools | `tool_web_search_error` | Web-search tool error | Ongoing compact + detail | Compact query/input plus structured error summary when available, error style. | Full input plus error output. |
| legacy-fallback | events/render | `tool_result` | Legacy/orphan result | Detail-only unless paired | First result line. | Full result text. |
| current-emitted | events/render | `tool_result_ok` | Tool result success | Detail-only unless paired | First result line. | Full result text. |
| current-emitted | events/render | `tool_result_error` | Tool result error | Detail-only unless paired | Full error text if direct error entry; otherwise structured error summary when available. | Full result text. |
| current-emitted | visibility/meta | `developer_context` | Developer metadata/context | Detail-only unless unknown-message visibility override applies | Message-type label or fallback. | Full context text. |
| current-emitted | visibility/styles | `developer_feedback` | Developer feedback/error-feedback message | Ongoing + detail | First non-empty line. | Full feedback text. |
| current-emitted | visibility/styles | `developer_error_feedback` | Operator-facing error feedback | Ongoing + detail | Full text. | Full text. |
| current-emitted | visibility/styles | `interruption` | User interrupt notice | Ongoing + detail | `You interrupted`. | Same text. |
| current-emitted | visibility/styles | `warning` | Warning/cache/pre-compaction | Detail-only by default; ongoing only when explicitly all-visible | Known warning label or first line. | Full warning text. |
| current-emitted | visibility/styles | `cache_warning` | Cache warning | Detail-only or configured visibility | First warning line. | Full warning text. |
| current-emitted | events/styles | `error` | Detail diagnostic/local error | Detail-only | Full text. | Full text. |
| current-emitted | visibility/events/styles | `system` | Background/system notice | Ongoing + detail if projected that way | `OngoingText`/compact content, else first line. | Full system text. |
| current-emitted | events/render | `compaction_notice` | Synthetic/local compaction notice | Ongoing + detail when present | `Context compacted for the Nth time`. | Same text. |
| current-emitted | visibility/render | `compaction_summary` | Persisted compaction summary | Detail-only | `Context compacted for the Nth time` or `Context compacted`. | Full summary. |
| current-emitted | visibility/render | `manual_compaction_carryover` | Manual compact carryover | Detail-only | `Last user message preserved for compaction`. | Full carryover content. |
| current-emitted | render/styles | `reviewer_status` | Reviewer local entry | Ongoing compact + detail | First status line. | Full status text. |
| current-emitted | render/styles | `reviewer_suggestions` | Reviewer local entry | Ongoing compact + detail | First suggestion line or `Reviewer suggestions`. | Full suggestions. |
| future-only | classifier | unknown role | Future/invalid transcript entry | Ongoing + detail when text exists; otherwise detail-only diagnostic | First non-empty line or `Unknown entry: <role>`. | Full recoverable text. |

## Legacy And Unknown Data

Detail mode must degrade safely for old sessions and partial transcript entries. Compact rendering must never depend on metadata that may be absent in persisted history.

Compatibility rules:

- If persisted `events.jsonl` still contains structured `llm.Message.MessageType`, `SourcePath`, `CompactContent`, tool-call presentation, or tool-call id, runtime projection should recover those fields into `ChatEntry` on replay. This is read-time projection, not a storage migration.
- If historical data truly lacks metadata, Builder does not backfill it by parsing message text. The collapsed label falls back by role, then by first non-empty rendered line, then by a generic role label. Expanding always reveals the full stored text.
- Legacy `developer_context` without `MessageType` renders as `Developer context` when its text is empty, otherwise first non-empty line. It is expandable to full content.
- Legacy AGENTS/skills/environment/worktree reminders without metadata are not reclassified by header/prefix matching. They use the legacy developer-context fallback.
- Legacy tool calls without `ToolCallMeta` render as generic tool calls. Collapsed label is first non-empty tool input line or `Tool call`. Do not derive an error summary from legacy output text. Expanded content is the stored call text plus matched result text if the result can be matched by adjacency or call id.
- Legacy patch calls without patch metadata are not rendered as rich patch summaries. Collapsed label is first non-empty tool input line or `Tool call`; expanded content is the stored patch text/result using plain tool rendering.
- Legacy background/system notices without `OngoingText` render as first non-empty system line or `System notice`.
- Legacy compaction summaries without count/generation render collapsed as `Context compacted`; expanded content shows the full summary.
- Legacy orphan tool results remain visible and expandable; they are not hidden because no matching call exists.

Unknown-type policy:

- Unknown `message_type` values are preserved in `ChatEntry.MessageType` and never dropped.
- Unknown developer message types project to `developer_context` with `MessageType` retained. Collapsed label is `Developer context: <message_type>` unless a server-provided compact label exists.
- Unknown message types keep their projected transcript role. If text is non-empty, they are visible in ongoing and detail. Collapsed label follows role fallback. The raw unknown type may be shown as faint inline metadata only if it fits without harming scanability.
- Unknown transcript roles are selectable and expandable. If text is non-empty, they are visible in ongoing and detail. Collapsed label is first non-empty rendered line; if empty, `Unknown entry: <role>`.
- Unknown tool presentation/render behavior falls back to plain tool rendering. It must not panic, discard input/output, or attempt shell/patch/question-specific rendering.
- Unknown tool result status defaults to neutral tool styling unless the role is explicitly `tool_result_error`.
- Unknown assistant phase renders as `assistant` unless it is explicitly `commentary`.
- Unknown visibility values with non-empty text should be treated as visible in ongoing and detail after runtime/client contract validation reports a diagnostic. Empty unknown-visibility entries are detail-only diagnostics. The renderer should not silently hide unknown entries in detail.

Diagnostics:

- Production builds should emit a render diagnostic for unknown roles, unknown message types, invalid tool metadata, missing selected-entry identity, and failed collapsed-label generation, then use fallback rendering.
- Debug mode may hard-fail only for invariants introduced by the new detail item model, e.g. duplicate item keys in one viewport, invalid entry ranges, or expanded-state keys that cannot map to any loaded item after reconciliation.
- Diagnostics must include structured fields where available: role, message_type, tool_name, tool_call_id, entry index, and source path.

The compatibility invariant: old sessions remain fully readable. They may lose rich collapsed labels, but they must not lose text, tool output, or the ability to expand.

## Classification Architecture

Implement one detail-entry classifier and make all collapsed rendering go through it. Avoid scattered role/type switches across viewport code, markdown rendering, and tool rendering.

Classifier inputs:

- Transcript role.
- Entry visibility.
- Source message type.
- Source path.
- Ongoing/compact text.
- Tool call metadata.
- Tool result role/text.
- Structured tool result summary.
- Entry text.
- Entry absolute range.

Classifier output:

- Detail kind: one of `user`, `assistant`, `reasoning`, `tool`, `shell_tool`, `patch_tool`, `web_search_tool`, `question`, `tool_orphan_result`, `reviewer`, `developer_context`, `compaction`, `background`, `system`, `warning`, `error`, `unknown`.
- Selectability: all transcript items are selectable except pure divider/fill lines.
- Collapse behavior: `three_line_preview`, `one_line_label`, `input_plus_error_summary`, `full_text_error`, or `same_as_expanded`.
- Stable collapsed label or label resolver.
- Expanded renderer id.
- Diagnostic facts for unknown/fallback cases.

Label precedence:

1. Error role with direct error text: full error text, no collapse.
2. Server-provided `CompactLabel`.
3. `OngoingText`/compact content.
4. Structured tool result summary for tool-error collapsed rows.
5. Tool metadata summary: patch summary, compact tool text, shell command first line, ask-question question.
6. Known source `MessageType` label.
7. Known transcript role label.
8. First non-empty rendered preview line.
9. Generic fallback label.

Exhaustiveness rules:

- Every `llm.MessageType` constant must appear in a classifier test as known or intentionally unknown.
- Every transcript role produced by `transcript_message_visibility.go`, local entries, tool rendering, reviewer rendering, and background notices must appear in a classifier test as known or intentionally unknown.
- Adding a new role/message type without classifier coverage should fail tests.
- Unknown tests must assert that non-empty unknown entries are visible in ongoing and detail, selectable, expandable, and diagnostic-producing.

Unspecified future message types:

- If the source role is `developer`, project as `developer_context`, preserve the unknown `MessageType`, show in ongoing and detail when text is non-empty, collapse as `Developer context: <message_type>`, expand full content.
- If the source role is `user`, preserve user rendering semantics. `compaction_summary` remains the only user message type with special compaction rendering unless a new typed rule is added.
- If the source role is `assistant`, preserve assistant rendering semantics and phase rules. Unknown assistant message types must not suppress content.
- If the source role is `tool`, preserve tool-result rendering semantics. Unknown tool message types must not suppress output.
- If the source role itself is unknown at the client boundary, normalize only enough to keep it visible as `unknown`; if text is non-empty, keep it visible in ongoing and detail.

## Legacy Compatibility Levels

Treat legacy support as explicit capability levels, so implementation and tests can assert expected behavior:

| Level | Available data | Expected detail behavior |
| --- | --- | --- |
| L0: current structured replay | Role, text, tool ids, tool metadata, message type, source path, compact content, structured result summary | Full compact labels and rich expansion. |
| L1: structured messages but old client contract | Runtime can read `llm.Message` fields from persisted events, but old `ChatEntry` did not expose them | Runtime projection recovers fields into new `ChatEntry`; behavior matches L0 after hydrate when source data exists. |
| L2: role/text plus tool ids | No source message type/path, partial tool metadata | Role-based compact labels; tool calls/results match by call id/adjacency where possible; expansion shows all stored text. |
| L3: role/text only | No metadata, no ids | Role fallback labels; no rich pairing beyond immediate adjacency; expansion shows full stored text. |
| L4: malformed/unknown entry | Missing/unknown role, invalid visibility, invalid tool metadata, or empty unknown data | Non-empty text is visible in ongoing and detail; empty entries become detail-only diagnostics; expansion shows any recoverable text. |

No level permits destructive migration or silent transcript omission. If an entry was previously visible in detail, it stays visible in detail.

## Label Wording Rules

Use stable, boring labels. Labels are for scanability, not decoration.

- Labels use sentence case, no trailing punctuation.
- Labels never include raw JSON, raw role names, or raw message types except unknown fallback labels.
- Unknown fallback labels may include ids/types to help debugging, e.g. `Developer context: custom_policy`.
- Paths are displayed from metadata after normal path presentation rules. If source path is absolute and under workspace, prefer repo-relative display when that helper exists; otherwise show absolute path. Do not parse paths out of content.
- Tool collapsed labels should not show output counts by parsing result text. Use structured patch/tool metadata only.
- Tool error collapsed labels must not derive summary text from first output line. They may show concise error summary only from structured runtime/projection field such as `ToolResultSummary`.
- If a preview line is longer than viewport width, truncate with ANSI-aware ellipsis. Do not wrap one-line labels except user/assistant 3-line previews.

## Required Data Model Changes

Current `developer_context` projection discards `MessageType` and `SourcePath`, which makes reliable collapsed labels impossible without content-prefix parsing. Add structured display metadata to the client transcript contract.

Suggested client metadata shape:

- `MessageType string`: source `llm.MessageType` when the transcript entry came from a model/runtime message.
- `SourcePath string`: authoritative source path when known, currently AGENTS.md.
- `CompactLabel string`: optional server-owned collapsed label for entries where the server has better structured context than the renderer.
- `ToolResultSummary string`: optional structured result summary for collapsed tool-error rows. Tool handlers or runtime adapters may provide the raw summary source; runtime projection owns copying it into `runtime.ChatEntry`; `server/runtimeview/projection.go` owns copying it into `clientui.ChatEntry`; TUI renderers must never compute it by scraping output text.
- `CompactionOrdinal int`: optional count for compaction summaries/notices when known.
- `Legacy bool`: optional flag set by projection when an entry is missing expected metadata. This is for diagnostics/tests only; rendering must not branch on it except to avoid noisy diagnostics.

Metadata is the first implementation slice. Add runtime projection and client-contract round-trip coverage before changing the detail renderer.

Do not implement this by regexing AGENTS.md headers, skill headings, worktree prompt text, or patch text. If the label needs structured data, carry that structure through runtime projection.

## Implementation Architecture

The implementation should land as a sequence of small, testable seams. Avoid a large renderer rewrite before metadata and classification are stable.

### Projection Layer

Owns transcript facts and compatibility. Candidate files:

- `server/runtime/chat_store.go`: extend runtime `ChatEntry` with message metadata and structured result summary.
- `shared/clientui/types.go`: extend client `ChatEntry` with the same DTO-safe fields.
- `server/runtime/transcript_message_visibility.go`: project `MessageType`, `SourcePath`, `CompactContent`, and context labels where runtime has authoritative facts.
- `server/runtime/transcript_event_entries.go`: preserve metadata for live event entries, tool started/completed rows, background notices, cache warnings, and local entries.
- `server/runtimeview/projection.go`: clone new metadata fields into client DTOs.
- `cli/tui/model.go`: carry metadata into `TranscriptEntry`.

Projection rules:

- Preserve structured source fields whenever persisted `llm.Message` or `ResponseItem` already contains them.
- Do not migrate `events.jsonl`; hydrate old rows through read-time projection.
- Do not parse raw content to recover missing metadata.
- `ToolResultSummary` is optional and runtime-owned. If no structured summary exists, omit it; the collapsed tool-error row then shows input only.
- Empty tool-error output with no structured summary renders as compact input with error styling and no synthetic summary.
- Error-only output without structured summary remains available only in expanded content; collapsed rendering must not use the first line as a summary.

### Classifier Layer

Owns table-driven decisions. Candidate files:

- `cli/tui/detail_classifier.go`: classify a `TranscriptEntry` or grouped call/result item into a detail kind, visibility expectation, collapsed behavior, expanded renderer, tree-guide state, and diagnostics.
- `cli/tui/detail_classifier_test.go`: exhaustive matrix tests for message types and transcript roles.

Classifier rules:

- The appendix tables are the source for expected tests.
- Classifier emits diagnostics for unknown/fallback paths, but returns a valid visible detail item whenever recoverable text exists.
- Classifier never renders markdown/diffs/tool text itself. It chooses render policy and labels only.
- Classifier never decides ongoing visibility for known entries. Known visibility comes from runtime projection and the locked visibility matrix.

### Detail Item Model

Owns grouping and identity. Candidate files:

- `cli/tui/detail_items.go`: convert transcript entries into detail items, pairing tool calls/results and combining reasoning blocks.
- `cli/tui/model_rendering.go`: consume detail items instead of raw `detailBlockSpec` where practical.

Detail item fields:

- `key`
- `entry_start`
- `entry_end`
- `role`
- `kind`
- `collapsed_behavior`
- `expanded`
- `selectable`
- `collapsed_label`
- `diagnostics`
- render closures or renderer ids for collapsed/expanded paths

Pairing rules:

- Prefer `tool_call_id` matches.
- Fall back to immediate adjacency only for legacy call/result rows without ids.
- Never pair a result across an intervening non-result transcript message.
- Orphan results remain their own selectable detail items.

### Renderer Layer

Owns line generation only. Candidate files:

- `cli/tui/detail_rendering.go`: collapsed/expanded detail item renderers.
- Existing markdown/code/tool renderers remain responsible for rich text, syntax, and patch rendering.

Renderer rules:

- Collapsed tool input reuses ongoing compact text helpers.
- Expanded tool rows use current full detail rendering, plus explicit input/output sections only where needed for clarity.
- Selection overlay is applied after content rendering and semantic styling.
- Selection overlay cannot mutate foreground colors and cannot override semantic backgrounds.

### State And Navigation

Owns selection/expansion, not rendering:

- `selectedDetailItemKey`
- `expandedDetailItemKeys`
- detail viewport anchor `{item_key, line_offset}`
- reconciliation on transcript refresh, width changes, paging, and expand/collapse

Keyboard behavior:

- `Up`/`Down`: previous/next selectable detail item.
- `Enter`: toggle selected item in expanded set.
- `PgUp`/`PgDn`: scroll viewport and then select the visible selectable item nearest the viewport center anchor.

### App/Mouse Layer

Owns terminal mode toggles:

- Ongoing remains mouse-neutral.
- Detail may enable alternate-scroll only while running as alt-screen overlay.
- Leaving detail restores alternate-scroll state even after errors/interruption.

## State Reconciliation

Detail state is UI-local, but transcript pages can refresh, resize, or shift base offsets. Reconcile deterministically:

- Selected item key survives refresh if the same key is present in the new detail item list.
- If selected key disappeared, select the nearest item by absolute entry index. Prefer the next item at or after the old entry index; otherwise select previous item.
- If there are no selectable items, clear selection.
- Expanded keys survive refresh only for keys still present. Drop stale expanded keys silently in production; debug mode may diagnose impossible key collisions.
- When expanding/collapsing the selected item, keep the selected item visible. If it was at top of viewport, keep its top anchored. If it was partially visible, scroll minimally to keep at least the collapsed/expanded header visible.
- When viewport width changes, recompute rendered line counts and preserve selected item plus intra-item line offset when possible. If width shrink makes offset invalid, clamp to item start.
- When transcript base offset shifts due to paging, absolute entry indices remain primary identity. Relative/local indices must never be used as persistent detail keys.
- Streaming deltas do not mutate open detail content except through existing static-snapshot behavior. If detail is intentionally refreshed, reconcile via same key rules.

Item key format should be structured, not string-concatenated ad hoc. Suggested fields:

- `entry_start`: absolute transcript entry index.
- `entry_end`: absolute transcript entry end index for grouped call/result or combined reasoning.
- `role`: normalized transcript role.
- `tool_call_id`: optional.
- `message_type`: optional.
- `synthetic_kind`: optional for streaming/local synthetic items without entry indices.

The key must be comparable and testable. If two items collide, production should append an internal sequence only for UI stability and emit a diagnostic; debug mode may hard-fail.

## Selection Contract

Selection is a visual overlay/line-fill layer applied after content rendering and semantic styling. It must:

- Pad every selected line to viewport width with selection background or equivalent selected fill.
- Preserve all existing foreground colors.
- Preserve semantic backgrounds such as diff add/remove backgrounds and syntax-highlighting backgrounds.
- Avoid rewriting ANSI escapes in ways that corrupt line width, wrapping, or reset behavior.

Regression snapshots must include selected user text, selected patch diff add/remove lines, and selected syntax-highlighted shell/tool output.

## Mouse Scope

Alternate-scroll wiring belongs to app/overlay lifecycle, not renderer. Detail may request terminal alternate-scroll only while running as an alt-screen overlay. Detail must not enable terminal mouse capture because it blocks native text selection in common terminals. Leaving detail must restore alternate-scroll state. Ongoing mode must not enable mouse capture or alternate-scroll, including after toggling detail open/closed.

## Minimum Test Matrix

Metadata/projection tests:

- Known developer message types preserve `MessageType`.
- AGENTS.md preserves `SourcePath`.
- Background notice preserves compact content.
- Unknown developer message type is preserved and projects to `developer_context` visible in ongoing and detail when text exists.
- Legacy event with message type still in JSONL but missing old client fields hydrates into new `ChatEntry` metadata.
- Legacy role/text-only entries render without metadata and expand full text.

Classifier tests:

- Every known `llm.MessageType` has expected detail kind/label policy.
- Every known transcript role has expected detail kind/label policy.
- Unknown message type/role/tool behavior produces diagnostic and visible expandable item.
- Error roles use full text collapsed and expanded.
- Tool error result stays collapsed by default and shows compact input plus structured error summary when provided; expansion shows full output.

Renderer tests:

- User and assistant collapsed previews show exactly first 3 rendered lines plus ellipsis when truncated.
- Tool collapsed preview matches ongoing compact input behavior.
- Tool error collapsed preview shows compact input plus structured error summary when provided and does not auto-expand.
- Tool error collapsed preview does not synthesize summaries from raw output text; empty/error-only outputs stay stable.
- Ask-question collapsed preview shows question only; expanded shows suggestions and answer.
- Patch collapsed preview shows `Edited...`; expanded shows full patch.
- Legacy developer context uses first non-empty line or `Developer context`.
- Selection spans full width for normal text.
- Selection preserves patch diff add/remove backgrounds.
- Selection preserves syntax-highlighted command/output foregrounds/backgrounds.

State/navigation tests:

- `Up`/`Down` navigate messages, not lines.
- `Enter` toggles selected item.
- Expanded state survives resize and transcript refresh when keys survive.
- Selected item reconciles to nearest item when old key disappears.
- Detail viewport anchors selected item during expand/collapse.
- Paging/base-offset shifts do not lose expanded state for retained absolute entries.

Mouse/app tests:

- Ongoing never enables mouse capture or `?1007`.
- Entering detail alt-screen enables alternate-scroll (`?1007`) but does not enable mouse capture (`?1000`, `?1002`, `?1003`, `?1006`).
- Leaving detail restores prior alternate-scroll state.
- Visiting detail and returning to ongoing does not change ongoing key behavior.

## Implementation Plan

1. Update locked decisions in `docs/dev/decisions.md`.
2. Extend `clientui.ChatEntry` and runtime projection with typed transcript display metadata: `message_type`, `source_path`, `compact_label`, `tool_result_summary`, optional compaction ordinal, and diagnostics-friendly legacy state.
3. Add projection/contract tests before renderer changes.
4. Implement detail classifier with exhaustive tests generated or mirrored from the appendix.
5. Refactor detail block specs into explicit detail item model: `{key, entry range, collapsed render, expanded render, selectable}`.
6. Add expansion state, selection state, and key handling: `Up`/`Down` scroll by rendered line and re-anchor selection to the viewport center, `Enter` toggles, `PgUp`/`PgDn` scroll by page and re-anchor selection to the viewport center.
7. Add collapsed renderers per matrix, reusing ongoing compact tool text functions where required.
8. Update detail viewport metrics to count rendered lines from collapsed/expanded states and preserve viewport anchor across toggles, resizes, paging, and new transcript snapshots.
9. Rework selection styling as overlay/line-fill layer so it spans full width while preserving foregrounds and semantic backgrounds.
10. Wire mouse handling only to detail alt-screen entry/exit; add regression coverage that ongoing remains mouse-neutral before and after visiting detail.
11. Update public docs if any user-facing mode/key documentation exists after implementation.

## Rollout Milestones

### Milestone 1: Metadata And Compatibility

Goal: no visual redesign yet; detail has enough structured facts to render correctly later.

Deliverables:

- Runtime/client `ChatEntry` metadata fields.
- Projection from live events and persisted replay.
- Contract tests for message type/source path/compact content/tool result summary.
- Legacy replay tests proving missing metadata degrades without parsing and without losing content.

Exit criteria:

- Existing TUI snapshots remain unchanged except for DTO internals.
- Unknown message type with text survives to client and is not dropped.
- AGENTS.md, skills, environment, worktree, background notice, compaction carryover all preserve enough metadata to classify without text parsing.

### Milestone 2: Classifier And Detail Items

Goal: stable table-driven model without changing user-visible detail layout yet, except diagnostics in tests.

Deliverables:

- Detail classifier.
- Detail item builder with structured keys.
- Exhaustive role/message-type tests based on appendix.
- Pairing tests for call/result by id, adjacency fallback, and orphan results.

Exit criteria:

- Every current emitted role/message type is classified.
- Unknown/future rows are visible and diagnostic-producing.
- No renderer code needs to switch directly on raw `MessageType`.

### Milestone 3: Collapsed Detail Rendering

Goal: default detail becomes compact.

Deliverables:

- Collapsed renderers for user/assistant 3-line previews.
- Collapsed tool renderers matching ongoing input previews.
- Structured tool-error summary display when available.
- Developer/context labels from metadata.
- Tree-style continuation guides for multi-line detail items.

Exit criteria:

- Large command/file outputs no longer dominate collapsed detail.
- Expanding any item restores full previous detail content.
- Tool-error collapsed rows do not parse output text.

### Milestone 4: Navigation And State

Goal: make compact detail usable.

Deliverables:

- `Up`/`Down` item selection.
- `Enter` expansion toggle.
- Multi-expand state.
- Viewport anchoring and reconciliation.
- Resize/paging/refresh tests.

Exit criteria:

- Selection never jumps unexpectedly across refresh/resize.
- Expanded state survives when item keys survive.
- Page scrolling still works.

### Milestone 5: Selection Overlay And Mouse Scope

Goal: polish interaction and avoid terminal regressions.

Deliverables:

- Lowest-priority selection background/fill overlay.
- Snapshot tests for selected patch and syntax-highlighted lines.
- Detail-only mouse handling lifecycle.
- Ongoing mouse-neutral regression tests.

Exit criteria:

- Selection spans full width.
- Selection never changes foreground.
- Diff/syntax backgrounds win over selection background.
- Ongoing still preserves native scrollback/selection behavior.

### Milestone 6: Docs And Cleanup

Goal: ship without stale docs or dead architecture.

Deliverables:

- Public docs/keybinding updates if applicable.
- Developer docs updated if implementation differs from this spec.
- Remove obsolete detail-specific line-selection code paths.
- Benchmark detail open/scroll on large transcripts.

Exit criteria:

- `./scripts/test.sh ./...` passes.
- `./scripts/build.sh --output ./bin/builder` passes.
- Detail open/scroll benchmarks do not regress beyond accepted threshold.
