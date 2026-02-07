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

38. **Turn atomicity is one model step.**

39. **Model-step transient failure retry is limited.**
   - Automatic retry with backoff, up to 2 attempts per model step.

40. **No automatic retry for `bash` process-launch failures.**

41. **Interrupt injects explicit resume context.**
   - On user interrupt, append developer/system message: `User interrupted you`.
   - Resume continues with the next user message.

42. **Post-interrupt UI returns to idle default state.**
   - Agent is stopped.
   - Input box is visible and ready.

43. **Queued `Ctrl+Enter` messages are in-memory only.**
   - Not persisted across app restart.

44. **Injected mid-run messages persist on delivery boundary only.**
   - Do not persist at enqueue time.
   - Persist when delivered after current tool call completion.

45. **Single user message size is unbounded in v1.**

46. **No disk-history compaction in v1.**

47. **Session directory names remain UUID-only for now.**
   - Future titles/aliases may be added later without breaking current layout.

48. **Patch tool allowed operations in v1 are add/update/move.**
   - File delete operation is disallowed.

49. **Any patch containing a file-delete block is rejected atomically.**
   - If `Delete File` appears anywhere, reject whole patch with no changes.

50. **Patch path scope is configurable; default is workspace-only.**
   - Default behavior rejects patch targets outside workspace root.

51. **All prompts are centralized in repo files.**
   - System prompt must live in a markdown file in the repository.

52. **Tool definitions are centralized in a single file.**
   - Names, descriptions, and parameter schemas are edited in one place.

53. **Auto-inject local `AGENTS.md` on first user message.**
   - On first user message only, scan current working directory for `AGENTS.md`.
   - If found, append its full contents as an additional user message before model execution.
   - Format must clearly indicate this is an instruction file injection (harness-style instruction block).

54. **`ask_question` is shared across model and runtime.**
   - Tool can be invoked by the model and by internal runtime policies.
   - Same UI surface/interaction flow is reused for both invocation sources.

55. **`ask_question` supports optional post-answer action binding.**
   - Answer handling may be passive (no automatic action) or active (trigger configured follow-up action).
   - Action binding is optional and decided per use case (for example approvals).

56. **`AGENTS.md` is read once per session.**
   - After first-message injection, further `AGENTS.md` changes are ignored until a new session starts.

57. **`ask_question` waits indefinitely for user input.**
   - No timeout defaulting and no automatic cancel.

58. **Subscription OAuth failure is terminal in v1 auth flow.**
   - Do not auto-fallback to API key when OAuth fails.

59. **Default tool working directory is process-start cwd (workspace root).**

60. **Tool execution concurrency in a model step is unbounded.**

61. **Parallel tool results are returned in model-declared call order.**
   - Calls may execute in parallel.
   - Results are appended/returned strictly in declared order to satisfy provider contracts.

62. **Parallel-step failure policy waits for in-flight completion.**
   - If one call fails, allow all currently running calls to finish.
   - Return ordered results after completion.

63. **Ordered-result buffering is strict and uncapped in v1.**
   - Buffer completed outputs until earlier declared calls resolve.
   - No memory cap safeguard in MVP.

64. **Detailed transcript retention is full raw by default.**
   - Persist complete raw detailed transcript for replay/debugging.

65. **Prompts/tool definitions are runtime-hardcoded via build embedding.**
   - Maintain source files in repo for editing.
   - Embed at build time into binary (no runtime file loading dependency).

66. **`ask_question` interaction model supports suggestions and freeform override.**
   - If suggestions exist: show option picker plus `none of the above`.
   - User can press `Tab` to open freeform input even after choosing an option.
   - If no suggestions: show freeform input directly.

67. **Runtime `ask_question` pauses active pipeline until answered.**

68. **Post-interrupt resume input must be explicit user text.**
   - No autogenerated resume message.

69. **Post-answer actions use a typed action registry.**
   - Each action has stable ID, payload schema, and handler.
   - Extensibility is achieved by adding new typed actions.

70. **Instruction precedence follows API role semantics (no custom override layer).**
   - Do not invent custom precedence rules beyond provider/API role behavior.

71. **Transcript order is immutable for cache stability.**
   - Never reorder past transcript messages.
   - For new sessions: system prompt in dedicated API section, then agent/developer content, then transcript messages.
   - Avoid runtime changes that alter ordering/caching characteristics mid-session.

72. **Core inference/session contract is fully locked after first API call.**
   - Lock model and core generation parameters.
   - Lock tool list and full tool schema/description snapshot.
   - Lock system prompt snapshot.

73. **Interrupt control message uses developer role.**
   - Inject `User interrupted you` as a developer-role message.

74. **Event identity uses monotonic sequence IDs plus wall timestamp.**

75. **Credential storage preference is OS secure store with MVP fallback.**
   - Preferred: OS keychain/secure credential store.
   - MVP fallback allowed: plain file if secure store integration is not feasible.

76. **Startup session UX shows recent sessions with pick-or-new flow.**

77. **No session event compression in MVP.**
   - Future note: async compression (e.g., zstd) can be revisited later.

78. **`bash` tool executes in non-TTY mode by default.**
   - Use process pipes, not PTY.

79. **Merged output stream policy is stdout+stderr combined.**
   - No stream-origin tags in merged output.

80. **Interrupt kill escalation is SIGINT then SIGKILL after 10s grace.**

81. **Per-call timeout override remains bounded.**
   - Model may override timeout up to max 1 hour.

82. **80% context policy allows one final handoff response, then blocks.**
   - Trigger at tool boundaries.
   - Inject handoff-style instruction requesting summary/next steps and prohibiting tools.
   - Runtime hard-blocks tool calls during this final response.
   - Persist final handoff response atomically as a normal step.

83. **Auth scope is global app-level, not per-session.**

84. **`ask_question` queue semantics are strict and simple.**
   - FIFO queue.
   - In-memory only (no persistence across restart).
   - Submitted answers are not editable.

85. **Unknown `ask_question` action ID is fatal in v1.**
   - Crash entire CLI on unknown action resolution.

86. **Auth is required before startup completion.**
   - Block startup until valid auth is configured.

87. **Large output truncation payload is standardized.**
   - Apply threshold per tool call total.
   - On overflow, send head+tail plus truncation metadata.
