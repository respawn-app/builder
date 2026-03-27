#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

usage() {
	cat <<'USAGE'
Usage: scripts/build.sh --output /path/to/builder [--version vX.Y.Z|X.Y.Z] [--package ./cmd/builder]

Builds a release-profile Builder binary using a static Go toolchain configuration.

Options:
  --output   Output path for the compiled binary.
  --version  Override the embedded Builder version. Defaults to BUILDER_VERSION or VERSION.
  --package  Main package to build. Defaults to ./cmd/builder.
USAGE
}

read_version() {
	local version="${BUILDER_VERSION:-}"
	if [ -z "$version" ] && [ -f VERSION ]; then
		version="$(tr -d ' \n' <VERSION)"
	fi
	printf '%s' "${version#v}"
}

output=""
package_path="./cmd/builder"
version=""

while [[ $# -gt 0 ]]; do
	case "$1" in
	--output)
		output="${2:-}"
		shift 2
		;;
	--version)
		version="${2:-}"
		shift 2
		;;
	--package)
		package_path="${2:-}"
		shift 2
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

if [ -z "$output" ]; then
	echo "--output is required" >&2
	usage >&2
	exit 1
fi

if [ -z "$version" ]; then
	version="$(read_version)"
fi
version="${version#v}"

mkdir -p "$(dirname -- "$output")"

ldflags=(-s -w)
if [ -n "$version" ]; then
	ldflags+=(-X "builder/internal/buildinfo.Version=${version}")
fi

env CGO_ENABLED="${CGO_ENABLED:-0}" \
	go build \
	-trimpath \
	-buildvcs=false \
	-ldflags "${ldflags[*]}" \
	-o "$output" \
	"$package_path"
