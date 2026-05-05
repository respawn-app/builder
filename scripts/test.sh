#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repo_root"

if [ "${BUILDER_TEST_INHERIT_ENV:-}" != "1" ]; then
    while IFS= read -r name; do
        case "$name" in
            BUILDER_TEST_INHERIT_ENV|BUILDER_TEST_TIMEOUT_SECONDS)
                ;;
            BUILDER_*)
                unset "$name"
                ;;
        esac
    done < <(compgen -e BUILDER_ || true)
fi

log_file="$(mktemp -t builder-go-test.XXXXXX.log)"
test_pid=""
cleanup() {
    rm -f "$log_file"
}
trap cleanup EXIT

terminate_test_process_group() {
    if [ -z "${test_pid:-}" ] || ! kill -0 "$test_pid" 2>/dev/null; then
        return
    fi
    kill -TERM "-$test_pid" 2>/dev/null || kill -TERM "$test_pid" 2>/dev/null || true
    sleep 2
    kill -KILL "-$test_pid" 2>/dev/null || kill -KILL "$test_pid" 2>/dev/null || true
}

handle_interrupt() {
    terminate_test_process_group
    exit 130
}

handle_term() {
    terminate_test_process_group
    exit 143
}

trap handle_interrupt INT
trap handle_term TERM

timeout_seconds="${BUILDER_TEST_TIMEOUT_SECONDS:-120}"
case "$timeout_seconds" in
    ''|*[!0-9]*)
        printf 'BUILDER_TEST_TIMEOUT_SECONDS must be a positive integer <= 120\n' >&2
        exit 2
        ;;
esac
if [ "$timeout_seconds" -le 0 ] || [ "$timeout_seconds" -gt 120 ]; then
    printf 'BUILDER_TEST_TIMEOUT_SECONDS must be a positive integer <= 120\n' >&2
    exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
    printf 'python3 is required to run tests with a wall-clock timeout\n' >&2
    exit 2
fi

args=("$@")
if [ ${#args[@]} -eq 0 ]; then
    args=(./...)
fi

python3 - "$log_file" "${args[@]}" <<'PY' &
import os
import sys

log_file = sys.argv[1]
args = sys.argv[2:]
os.setsid()
fd = os.open(log_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
try:
    os.dup2(fd, 1)
    os.dup2(fd, 2)
finally:
    os.close(fd)
os.execvp("go", ["go", "test", *args])
PY
test_pid=$!
timed_out=0
deadline=$((SECONDS + timeout_seconds))

while kill -0 "$test_pid" 2>/dev/null; do
    if [ "$SECONDS" -ge "$deadline" ]; then
        timed_out=1
        terminate_test_process_group
        break
    fi
    sleep 1
done

set +e
wait "$test_pid"
status=$?
set -e
if [ "$status" -eq 0 ]; then
    exit 0
fi

if [ "$timed_out" -eq 1 ]; then
    printf 'test suite exceeded %ds wall-clock cap; simplify or speed up tests before continuing\n' "$timeout_seconds"
elif [ "$status" -eq 143 ] || [ "$status" -eq 137 ]; then
    printf 'test process was terminated by a signal (exit status %d)\n' "$status"
fi
cat "$log_file"
exit 1
