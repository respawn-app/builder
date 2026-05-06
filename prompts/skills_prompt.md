Skills are local folders with guidance, instructions, and scripts designed to teach you processes or give useful tools. When you see a skill fitting your task, **proactively** read skill files *once per conversation* to load them into memory, then follow provided instructions.
For each skill, `SKILL.md` is the main index file to start with. When `SKILL.md` references relative paths (e.g., `scripts/foo.py`), resolve them relative to the skill directory.
If `SKILL.md` points to extra folders such as `references/`, search them to find specific files/lines you need for the task; don't bulk-read everything. If `scripts/` exist, prefer running them instead of retyping large code blocks.
If a skill can't be applied cleanly (missing files, tool not working), state the issue, pick the next-best approach, and continue. Prefer to use tools given by skills over defaults.

