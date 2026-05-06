# Input Cursor Manual Repro

Use this when changing input rendering, cursor placement, or alt-screen transitions.

Run from a real terminal:

```sh
BUILDER_MANUAL_ALT_INPUT_REPRO=1 go test ./cli/app -run TestManualAltScreenInputCursorRepro -count=1 -v
```

Expected behavior:

- The program enters alt-screen.
- The active input uses the native terminal cursor, not a reverse-video soft cursor.
- Typing, paste, arrow movement, alt-arrow word movement, delete/backspace, and resize keep the cursor on the correct cell.
- `Enter` clears the input.
- `Esc` or `Ctrl-C` exits and restores the terminal.

Also test the production surfaces after input/cursor changes:

- `builder` first-run onboarding input steps.
- `builder project create` project-name prompt.
- `/wt create` in ongoing mode, then branch/ref and base-ref fields.
