# Decisions Log

This file records architecture and product decisions for the minimal terminal coding agent.

## Locked Decisions

1. **No TypeScript.**
   - Rationale: avoid TS TUI stack tradeoffs and runtime/tooling complexity for this project.

2. **No MCP, plugins, subagents, or skills in v1.**
   - Rationale: keep the system minimal and focused on core quality.

3. **No sandbox in v1 (full access).**
   - Rationale: maximize capability and reduce friction; add security controls later.

4. **Pluggable/composable architecture is required.**
   - Rationale: adding tools and event handlers should require minimal setup changes.
   - Notes: target Chain of Responsibility + interceptor/composite-style extension points.

5. **3 core tools in v1: `bash`, `patch`, `ask_question`**
   - `bash`: execute commands with the user's real PATH/environment, plus non-interactive markers.
   - `patch`: freeform patch application equivalent in behavior to `apply_patch`.
   - `ask_question`: asks the user a question and waits for input. Will be used for prompts, permissions, decisions, planning etc.

6. **Sessions must support stop/resume.**
   - persist conversation/tool history into JSON files in the user's home directory.

7. **Name is not final; must be easily changeable.**
   - Working name: `builder`.

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

15. **Session persistence format uses split files.**
   - Use `session.json` + `events.jsonl` per session.

16. **Runtime model is single-run per program instance.**
   - One app instance equals one active conversation/run.

17. **In-turn user messaging supports both injection and queueing.**
   - Mid-run user message injection is supported.
   - Queued post-turn send is supported via `Ctrl+Enter`.

18. **Mid-run injection policy is soft-insert only.**
   - Injected messages are delivered at the next safe boundary after current tool call completion.
   - No forced interruption for injected messages.

19. **Pending user message order is strict FIFO.**

20. **Pending message queue is unbounded.**

21. **`Ctrl+C` interrupt scope is turn-local.**
   - Interrupt current model step and active tool process.
   - Keep app/session alive.

22. **`bash` tool uses user login shell.**

23. **Tool timeout policy is bounded with model override.**
   - Default command timeout is 5 minutes.
   - Model may override timeout per tool call.

24. **Non-zero command exits are recoverable signals.**
   - Do not auto-abort turn on non-zero exit.
   - Let model handle recovery.

25. **Shell execution model is stateless per command.**
   - No persistent shell state between `bash` tool calls.

26. **Large tool output is bounded for model consumption.**
   - Configurable threshold (example baseline: 10k chars).
   - If exceeded, provide first 500 chars + last 500 chars to model.
   - Reduce noisy terminal behavior using non-interactive env hints (for example `TERM=dumb` and similar flags).

27. **Patch application is atomic only.**
   - On malformed patch or apply inconsistency/conflict, do not modify files.
   - Return clear failure reason to model.

28. **OpenAI auth in v1 supports both paths.**
   - API key auth.
   - Subscription OAuth auth.

29. **Model selection is session-initial and then locked.**
   - User can choose model only before first model/API turn.
   - After first turn, model selection is locked.

30. **Tool list/config is session-initial and then locked.**
   - Tool availability/config chosen before first model/API turn.
   - Lock after first turn to maximize cache hits.

31. **Session-start tool defaults are enabled.**
   - `bash=on`, `patch=on`.

32. **Approval policy in v1 is fully autonomous.**
   - No approval prompts for tool execution.

34. **Persistence root is configurable with workspace-scoped layout.**
   - Default root dir: `./agents/builder/`.
   - Workspace container: `<workspace-folder-name>-<random-uuid>`.
   - Session folders inside workspace container use UUID names.
   - This supersedes earlier home-dir-only assumption from Decision 6.

35. **Context overflow behavior is explicit stop.**
   - At 80% context usage, stop and ask user to start a new session.
   - No auto-compaction/summarization in MVP.

36. **Durability strategy is async with atomic turn writes.**
   - Capture runtime asynchronously.
   - Persist atomically at turn boundaries.

37. **Crash-loss tolerance allows losing in-flight work.**
   - Acceptable to lose up to one in-flight tool call on crash.
