#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

read_version() {
  local version="${BUILDER_VERSION:-}"
  if [ -z "$version" ] && [ -f VERSION ]; then
    version="$(tr -d ' \n' < VERSION)"
  fi
  printf '%s' "${version#v}"
}

run_format() {
  echo "==> verify formatting"
  local unformatted
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then
    echo "The following files are not gofmt-formatted:"
    echo "$unformatted"
    exit 1
  fi
}

run_vet() {
  echo "==> go vet"
  go vet ./...
}

run_build() {
  echo "==> go build"
  local version
  version="$(read_version)"
  if [ -n "$version" ]; then
    go build -ldflags "-X builder/internal/buildinfo.Version=${version}" -o ./bin/builder ./cmd/builder
    return
  fi
  go build -o ./bin/builder ./cmd/builder
}

run_test() {
  echo "==> go test"
  ./scripts/test.sh ./...
}

mode="${1:-all}"

case "$mode" in
  all)
    run_format
    run_vet
    run_build
    run_test
    ;;
  format)
    run_format
    ;;
  vet)
    run_vet
    ;;
  build)
    run_build
    ;;
  test)
    run_test
    ;;
  *)
    echo "Unknown mode: $mode" >&2
    echo "Usage: $0 [all|format|vet|build|test]" >&2
    exit 1
    ;;
esac
