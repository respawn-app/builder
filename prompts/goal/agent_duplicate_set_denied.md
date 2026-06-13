Current goal (status: {{.Status}}):
<goal>
{{.Objective}}
</goal>

Overwriting an existing goal is not allowed. Continue working on the current goal. If it is fully complete, run `{{.BuilderCommand}} goal complete`. If you need to inspect it again, run `{{.BuilderCommand}} goal show`. If you are blocked or unable to complete the goal, use `ask_question` to ask the user for help. Do not call `{{.BuilderCommand}} goal set` again while this goal remains active or paused.
