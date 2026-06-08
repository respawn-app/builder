#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/export-reviewer-request.sh <project-session-dir|events.jsonl> [output-prefix]

Exports exactly what Builder's supervisor/reviewer would receive for the
current persisted session state.

Outputs:
  <prefix>.json  Raw OpenAI Responses request body
  <prefix>.md    Readable markdown projection of the same request

If output-prefix is omitted, files are written under the repo-local temp dir as:
  .tmp/reviewer-exports/reviewer-request-<session-id>-<timestamp>.json
  .tmp/reviewer-exports/reviewer-request-<session-id>-<timestamp>.md
EOF
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage >&2
  exit 1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)

input_path=$1
output_prefix=${2-}

cd "$repo_root"

if [[ -n "$output_prefix" ]]; then
  exec go run ./.tmp/export-reviewer-request.go --output-prefix "$output_prefix" "$input_path"
fi

exec go run ./.tmp/export-reviewer-request.go "$input_path"
