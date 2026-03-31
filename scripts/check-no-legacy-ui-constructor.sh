#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

matches="$(rg -n "NewUIModel\\(" cli/app || true)"
if [ -n "$matches" ]; then
	echo "legacy UI constructor usage detected; cli/app must use NewProjectedUIModel(...) only" >&2
	echo "$matches" >&2
	exit 1
fi
