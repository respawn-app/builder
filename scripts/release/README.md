# Builder 2.0 compat release (manual, one-off)

This branch (`builder-2.0-compat`) is the **pre-rebrand Builder codebase plus**
the `builder migrate` command and the refuse-to-start gate. It ships **once** as
**Builder 2.0** so existing Builder users can move to Kent. It is published
**manually** — never by the autorelease workflow (which now produces Kent only).

## What the compat binary does

- Refuses to start. The only commands that run are:
  - `builder migrate` — move `~/.builder` → `~/.kent`, rebase structured paths
    (DB `worktrees.canonical_root_path`, session `worktree_path`/`effective_cwd`),
    `git worktree repair` each moved worktree, repoint internal symlinks, verify,
    create the `~/.builder → ~/.kent` compat symlink, and drop `.generated`.
    Supports `--dry-run`.
  - `builder service uninstall` — remove the old background service.
- Every other invocation prints the migration notice and exits non-zero.

`migrate` snapshots the SQLite DB and every `session.json` into
`~/.kent/.migrate-backup/` before mutating, never reads/writes `events.jsonl`,
and on a failed verification leaves state + snapshot in place WITHOUT creating
the compat symlink (no auto-rollback, by design).

## Build the compat binary

```sh
BUILDER_SKIP_FRONTEND=1 ./scripts/build.sh --output ./builder_2.0.0_<os>_<arch>
```

`VERSION` on this branch is `2.0.0`, so the embedded version is correct. The
binary is CLI-only; the frontend build is skipped.

## Publish (manual)

1. Tag this branch `builder-2.0.0` and push the tag to `respawn-llc/kent`.
2. Build per-OS/arch binaries (macOS, Linux, Windows) as above and create a
   **separate GitHub release** on the `builder-2.0.0` tag with
   **`make_latest=false`** so it can never hijack Kent's `/latest`.
3. Consumers reach the compat binary via a **hardcoded pinned asset URL**
   (`…/releases/download/builder-2.0.0/builder_2.0.0_<os>_<arch>…`), never
   `/latest`. Optionally front it with a stable `kent.sh`/docs redirect.
4. Homebrew: publish `builder-cli.rb` (in this directory) to
   `respawn-llc/homebrew-tap/Formula/builder-cli.rb`, filling in the
   `builder-2.0.0` source tarball sha256. It is `deprecate!`d, builds the compat
   binary, and must **not** `depends_on "kent"`.

## Retirement (~1 month)

Keep `builder-cli` `deprecate!`d (installable, warns) and the compat release
asset live during the migration window, then `disable!` the formula, then remove
it. Abandon this branch afterward — nothing to delete from `main`/Kent.
