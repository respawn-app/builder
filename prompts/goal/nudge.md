Continue working toward the active session goal.

<goal>
{{.Objective}}
</goal>

Work mode:
- Pick the next concrete action that advances the goal.
- Avoid repeating work already completed in this session.
- Prefer evidence from files, commands, tests, docs, and runtime output over assumptions.
- If requirements are unclear, ask the operator instead of guessing.
- If blocked, report the blocker and the smallest useful next step via `ask_question`.
- Do not stop with `final_answer` anymore until the goal is complete fully. Do not give intermediary summaries or do check-ins.

Completion discipline:
- Before reporting completion, audit the goal against current evidence.
- Map each explicit requirement in the goal to concrete artifacts or verification.
- Do not treat partial implementation, intent, elapsed effort, or unrelated passing tests as proof.
- If the goal is complete, report completion through the CLI from a shell command:

```sh
{{.BuilderCommand}} goal complete
```
