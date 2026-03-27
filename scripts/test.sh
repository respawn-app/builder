#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

log_file="$(mktemp -t builder-go-test.XXXXXX.log)"
cleanup() {
	rm -f "$log_file"
}
trap cleanup EXIT

args=("$@")
if [ ${#args[@]} -eq 0 ]; then
	args=(./...)
fi

if go test "${args[@]}" >"$log_file" 2>&1; then
	exit 0
fi

cat "$log_file"
exit 1
