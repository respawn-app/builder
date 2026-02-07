# Decisions Log

This file records architecture and product decisions for the minimal terminal coding agent.

## Locked Decisions

1. **No TypeScript.**
   - Rationale: avoid TS TUI stack tradeoffs and runtime/tooling complexity for this project.

2. **No MCP, plugins, or skills in v1.**
   - Rationale: keep the system minimal and focused on core quality.

3. **No sandbox in v1 (full access).**
   - Rationale: maximize capability and reduce friction; add security controls later.

4. **Pluggable/composable architecture is required.**
   - Rationale: adding tools and event handlers should require minimal setup changes.
   - Notes: target Chain of Responsibility + interceptor/composite-style extension points.

5. **Two core tools in v1: `bash` and `patch`.**
   - `bash`: execute commands with the user's real PATH/environment, plus non-interactive markers.
   - `patch`: freeform patch application equivalent in behavior to `apply_patch`.

6. **Sessions must support stop/resume.**
   - Rationale: persist conversation/tool history into JSON files in the user's home directory.

7. **Name is not final; must be easily changeable.**
   - Working name: `agent`.

8. **Stack for v1: Go + Bubble Tea.**
   - Rationale: best speed-to-polished UX, responsive TUI model, strong process orchestration, simple distribution.

9. **UI has two modes: `ongoing` and `detail`.**
   - `ongoing` is the default mode.
   - `detail` is toggled via hotkey.

10. **`ongoing` mode is strictly minimal.**
   - Show only command start and file hint when available (with truncation/ellipsis).
   - If collapsing is not possible, show first command line only and ellipsize.
   - Do not show thinking traces, preambles, outputs, or diffs.

11. **`detail` mode shows full available visibility.**
   - Show all model-visible streaming text/tokens.
   - Show all available trace signals (raw and/or summarized when exposed by provider/runtime).
   - Show full tool calls, outputs, patches, and diffs with scrollback.

12. **Streaming and history visibility are always required.**
   - While the agent works, tokens/tool events stream continuously and remain visible with scrollback.

13. **Fallback for detail-mode complexity is accepted.**
   - If maintaining rolling stream + stable manual scroll is problematic, `detail` may switch to snapshot behavior:
   - User views a non-updating snapshot in `detail`; new events become visible after re-entering.

14. **Hotkeys are fixed in v1 (not configurable).**
   - `Tab`: toggle `ongoing`/`detail`.
   - `Ctrl+C`: interrupt current work.
   - `Ctrl+R`: not used.
