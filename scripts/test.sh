#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

if [ "${BUILDER_TEST_INHERIT_ENV:-}" != "1" ]; then
    while IFS= read -r name; do
        case "$name" in
            BUILDER_TEST_INHERIT_ENV)
                ;;
            BUILDER_*)
                unset "$name"
                ;;
        esac
    done < <(compgen -e BUILDER_ || true)
fi

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
