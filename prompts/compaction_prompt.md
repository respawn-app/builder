Produce a **handoff document** for the next AI agent right now as your next response. The next agent:

- Will not see this conversation.
- Will only see: your next message with the handoff response + the environment (AGENTS.md).
- Must be able to continue your work seamlessly.

Handoff structure: 
---

## 1. Overall task and goal summary
- In plain text and product language, describe what the user ultimately wants in business/feature terms. This section should preserve information from previous handoffs, if any, and expand on them.

## 2. Current status (done / in progress / not started)
Provide a bullet list of subtasks with status:

- For each subtask:  
  `- [STATUS] <short name> — <1-line description>`
  - STATUS is one of: `DONE`, `IN_PROGRESS`, `NOT_STARTED`.
- Explicitly state where you stopped working:
  `Last change: <very short summary> in <file path> around <function / class / section>`.

## 3. Relevant file paths and directory structure
List everything that is needed for the task in full. Help the next agent preserve and gather context for the continuation of the work (since they will not see or remember file content, they will re-read the files per your instructions here).

- List key directories and their roles, focusing on what this task touches. For example:  
  `- src/.../metrics/ — runtime metrics collection`  
  `- src/.../ui/featureX/ — screens affected by this change`
- List key files (with full relative paths) that are important to understand or modify for this task, with a 1-line description each:
  `- src/.../FeatureXViewModel.kt — main state machine for Feature X`

## 4. Architecture and key design concepts for this task
Summarize only the parts of the architecture relevant to this work:

- Describe the main architectural pattern(s) involved, and how this task fits into them.
- List important invariants, constraints, and rules that must not be violated (e.g., “use thread safety primitives”, “API must be the single source of truth”).
- Point to core contracts (interfaces, abstract classes, protocols) that define behavior relevant to this task (with file paths).

## 5. Files and components changed in this session
For every file you modified, added, or deleted since last handoff, if present:

- Provide a bullet point:  
  `- <file path> — <1-sentence summary of what changed and why>`
- Mention any new public APIs, classes, or functions that other parts of the system now depend on, and describe their purpose.

## 6. Remaining work (ordered action plan)
Describe what the next agent should do in **small, executable steps**, in the order you recommend:

- Use a numbered list.
- For each step, include:
  - The goal of the step.
  - The main files/classes/functions to touch.
  - Any preconditions (e.g., “Do step 2 only after step 1 tests pass”).
- Keep steps concrete (things that can be done in one short focus session), not vague tasks like “refactor code”.
- Don't make up plans or instructions, base this section on what you were **already** doing (per user) when this handoff was requested. If the action plan was written elsewhere (in docs etc.), point the next agent to the file.

Examples:
1. Implement X in `<file>` by doing Y.
2. Update tests in `<test file>` to cover cases A, B, C.
3. Manually verify scenario Z using `<command>` or `<UI flow>`.

If no pending work is present, replace this section with "No pending work to execute right now"

## 7. Current test and runtime status (optional, if tests are involved in the task)
- Concisely list/summarize existing tests failures before this handoff.
- Note any manual testing flows you did or that will be relevant, and any failures detected during them that are not yet addressed.

## 8. Known issues, limitations, and edge cases
List **everything** that you know is still problematic or incomplete:

- Bugs discovered but not fixed (with brief description and affected components).
- Edge cases that are not handled, partially handled, or intentionally ignored (with reasoning).
- Performance, security, or compatibility concerns identified during your work.
- Trade-offs and limitations imposed onto current code by your work or overall

Be explicit if some issues are acceptable trade-offs and should remain, for example, if the user confirmed they should. If there are any actions to be taken on the listed points, instruct the next agent on how to do that.

## 9. Open questions and assumptions
Capture anything ambiguous the next agent must be aware of:

- Questions that came up but are still unanswered.
- Assumptions you had to make about requirements, API behavior, UX, or constraints.
- Format with this structure:  
  `<explanation> [rationale: <why you chose this>; (optional, if you made a decision)]`

If you suggest defaults the next agent should keep, state them clearly. For each open question or assumption, issue a recommendation to either ask the user for clarification if the assumption cannot be resolved on your own, or how to resolve it. Do not include in this section the historical assumptions that were already resolved or became irrelevant during the session.

## 10*. Interfaces to external systems (optional, if applicable)
Briefly describe any external dependencies this work touches (if applicable):

- APIs, services, databases, queues, or third-party SDKs involved.
- Important endpoints, schemas, or contracts (just enough for the next agent to reason about correctness).

Avoid restating already-known or standard interfaces like "app database" or "git". Mention **new** interfaces relevant for the task that the next agent might not be aware of, like logging systems user mentioned or document locations.

## 11. Intentional shortcuts and technical debt introduced
If you introduced any deliberate shortcuts, skipped work, added hacks, cut work short or omitted some pieces of the final implementation, document them:

- Location: file + code paths.
- What shortcut was taken.
- Why it was taken (e.g., time constraints, low risk, complexity)
- recommended resolution or approach for the next agent (ask user/resolve/mark as todo etc).

Treat it like asking the next agent "hey, since you pick up after me, please handle this for me / help me with this"

## 12. User requests and past design decisions
Thoroughly document the history of relevant decisions so the next agent does not repeatedly ask the user same questions or make dangerous assumptions:

- Summarize key user preferences and explicit decisions that constrain implementation, especially architectural, product, UX ones:
  - Chosen patterns, interactions, or libraries.
  - Rejected alternatives and why they were rejected.
- Capture important past decisions that affect the final implementation:
  - What was decided.
  - When / in what context (high level only).
  - The reasoning and trade-offs.
- Clearly mark decisions that **must not be changed** without explicit user approval.

Example:

> "Maintain a separate store for audio recording functionality; reason: user requested this after we established that AudioRecorder API is synchronous and imperative, limiting integration ability."

## 13. Mistakes made and how they can be/were solved/prevented 
Fully list mistakes that were made that required correction during the session and explain why the user or you (the agent) labeled them as such, how they were corrected, or if they weren't how they can be corrected, and then how they should be prevented by the next agent. Examples: failed tool calls, user getting angry/correcting you, reverts of code, pivots in implementation. Maintain imperative tone, e.g. "User strongly asserted that no backward compatibility is preserved: do not add fallbacks and deprecation notices."

---

# Style and constraints for the handoff document
- Your handoff will **not** be exposed to users; it will be used only as internal reasoning document for the next agent. Use same terse style as for the `analysis` channel.
- Prefer specifics (relative paths from cwd, function names, commands) over vague descriptions.
- Avoid restating generic project documentation or common knowledge
- Do not follow verbosity instructions that apply outside of this task, like the `Desired oververbosity for the final answer` setting. Each section provides guidance on verbosity you should follow.
- Do not include any overall document headers like "# HANDOFF".
- Do NOT repeat the content or instructions from AGENTS.md files in your handoff or system instructions (the next agent will receive those). However, treat instructions from the user (as User messages in this conversation) as essential to preserve the meaning of.

# Coherent continuation of work 
If this conversation started with another handoff (meaning that you're not the first agent to work on the task), then **preserve** and carry over **all** relevant and important information from the original handoff. To maintain coherence and drive the **overall** task to completion, the handoff you produce must be a continuation of the **overarching** task **in addition** to the chunk you completed in this session. For example, preserve original user goals in section 1 and add additional information learned from the current session instead of removing that from the scope; preserve previous mistakes/learnings and add new ones instead of only including the latest info. This applies to all other sections as well. Assume that the information you received in the handoff at the beginning is trustworthy, with additions and corrections from the user (if any) from this session taking priority.

 ---

Now, immediately output the final document and nothing else (no commentary, acknowledgement, talking to the user, questions, tool calls, preambles or anything else). Output strictly the complete handoff content as final answer and end your turn.
