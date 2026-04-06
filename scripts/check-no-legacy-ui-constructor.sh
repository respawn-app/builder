#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

search_pattern='NewUIModel('
if command -v rg >/dev/null 2>&1; then
	matches="$(rg -n -F "$search_pattern" cli/app || true)"
else
	matches="$(grep -RFn "$search_pattern" cli/app || true)"
fi
if [ -n "$matches" ]; then
	echo "legacy UI constructor usage detected; cli/app must use NewProjectedUIModel(...) only" >&2
	echo "$matches" >&2
	exit 1
fi
