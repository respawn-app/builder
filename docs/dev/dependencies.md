# Dependency Updates

Repository dependency updates currently use `scripts/update-deps.sh`.

Supported ecosystems:

- Root Go module (`go.mod`, `go.sum`) via `go get -u -t ./...` followed by `go mod tidy`.
- Docs site dependencies (`docs/package.json`, `docs/pnpm-lock.yaml`) via `pnpm up --latest`.

GitHub Actions version pins are managed separately and are intentionally excluded from this script.

Examples:

```bash
scripts/update-deps.sh
scripts/update-deps.sh --dry-run
scripts/update-deps.sh --skip-docs
```
