#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

usage() {
	cat <<'USAGE'
Usage: scripts/update-deps.sh [--dry-run] [--skip-go] [--skip-docs]

Updates repository dependencies for the currently supported package managers:
  - root Go module (`go.mod` / `go.sum`)
  - docs pnpm workspace (`docs/package.json` / `docs/pnpm-lock.yaml`)

GitHub Actions version pins are intentionally excluded from this script.

Options:
  --dry-run    Print planned update commands without executing them.
  --skip-go    Skip Go module dependency updates.
  --skip-docs  Skip docs pnpm dependency updates.
USAGE
}

dry_run="false"
skip_go="false"
skip_docs="false"

while [[ $# -gt 0 ]]; do
	case "$1" in
	--dry-run)
		dry_run="true"
		shift
		;;
	--skip-go)
		skip_go="true"
		shift
		;;
	--skip-docs)
		skip_docs="true"
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Unknown argument: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

run_cmd() {
	if [[ "$dry_run" == "true" ]]; then
		printf '[dry-run]'
		printf ' %q' "$@"
		printf '\n'
		return
	fi
	"$@"
}

require_cmd() {
	local cmd="$1"
	if command -v "$cmd" >/dev/null 2>&1; then
		return
	fi
	echo "Required command not found: $cmd" >&2
	exit 1
}

updated_any="false"

update_go_deps() {
	if [[ "$skip_go" == "true" ]]; then
		return
	fi
	if [[ ! -f "$repo_root/go.mod" ]]; then
		return
	fi
	require_cmd go
	echo "==> Updating Go module dependencies"
	run_cmd go get -u -t ./...
	run_cmd go mod tidy
	updated_any="true"
}

update_docs_deps() {
	if [[ "$skip_docs" == "true" ]]; then
		return
	fi
	if [[ ! -f "$repo_root/docs/package.json" ]]; then
		return
	fi
	require_cmd pnpm
	echo "==> Updating docs pnpm dependencies"
	run_cmd pnpm --dir "$repo_root/docs" up --latest
	updated_any="true"
}

update_go_deps
update_docs_deps

if [[ "$updated_any" != "true" ]]; then
	echo "No supported dependency manifests found to update."
fi
