The user set an active session goal.

<goal>
{{objective}}
</goal>

Start working toward this goal now.

Work mode:
- Pick the next concrete action that advances the goal.
- Avoid repeating work already completed in this session.
- Prefer evidence from files, commands, tests, docs, and runtime output over assumptions.
- If requirements are unclear, ask the operator instead of guessing.
- If blocked, report the blocker and the smallest useful next step via `ask_question`.
- Before starting work, prepare for this goal's completion to take multiple handoffs; Since context will be lost on handoff, lock the specification, plans, user intent, product decisions in documentation files, if haven't yet.

Completion discipline:
- Before reporting completion, audit the goal against current evidence.
- Map each explicit requirement in the goal to concrete artifacts or verification.
- Do not treat partial implementation, intent, elapsed effort, or unrelated passing tests as proof.
- If the goal is complete, report completion through the Builder CLI from a shell command:

```sh
builder goal complete
```
