# Release

This is the current release flow for `builder`.

## Recommended Path

Use `workflow_dispatch`. It is the simplest path and does not require the `autorelease` PR label flow.

1. Make sure the release commit is on `main` and pushed.
2. Set `VERSION` to the release version, usually without the `v` prefix, for example:

```text
0.2
```

3. Commit and push that change.
4. Trigger the release workflow:

```bash
gh workflow run release.yml --repo respawn-app/builder
```

5. Wait for the `release` workflow in `respawn-app/builder` to finish.
6. Wait for the tap automation in `respawn-app/homebrew-tap` to finish.
7. Verify the GitHub release and Homebrew install.

## What The App Release Workflow Does

The `release` workflow in `/.github/workflows/release.yml`:

1. Reads `VERSION`.
2. Normalizes it to a git tag `vX.Y`.
3. Creates and pushes the tag if it does not already exist.
4. Builds the release archives.
5. Publishes the GitHub release.
6. Checks out `respawn-app/homebrew-tap`.
7. Runs `scripts/update-brew-tap.sh` for formula `builder-cli`.
8. Opens a PR in the tap repo with label `pr-pull`.

## What The Tap Automation Does

The tap repo automation is part of the release, not an optional follow-up.

1. The tap PR runs `brew test-bot`.
2. On success, `brew pr-pull` runs.
3. `brew pr-pull` pushes bottle metadata to tap `master`.
4. After that, `brew update && brew install builder-cli` should resolve to the new version.

## Verification

Verify all of these before considering the release done:

1. The GitHub release `vX.Y` exists in `respawn-app/builder` and contains the expected assets.
2. The tap PR in `respawn-app/homebrew-tap` is closed by the automation.
3. The formula on tap `master` has the new tag URL and bottle block.
4. A fresh Homebrew install works after `brew update`:

```bash
brew update
brew tap respawn-app/tap
brew install builder-cli
builder --version
```

If short-name resolution is stale on a machine, use the fully qualified formula name:

```bash
brew install respawn-app/tap/builder-cli
```

## Notes

- Installed binary name stays `builder`. Formula name is `builder-cli`.
- Do not create the git tag manually unless you are intentionally bypassing the workflow behavior.
- Short-name Homebrew installs may require `brew update` on machines with stale tap metadata.
- Future formula dependency changes are driven by `scripts/update-brew-tap.sh`. For example, `git` and `ripgrep` will start applying from `0.2+` because they were intentionally not backported into `0.1`.

## Alternate Path

The workflow can also run automatically when a merged PR carries the `autorelease` label. That path uses the same workflow and the same downstream tap automation.
