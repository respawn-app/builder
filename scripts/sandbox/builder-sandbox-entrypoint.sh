#!/usr/bin/env bash

set -euo pipefail

workspace_root="${SANDBOX_WORKSPACE_ROOT:-/workspace}"
seed_root="${SANDBOX_SEED_ROOT:-/opt/builder-sandbox-seed}"
config_seed_path="${SANDBOX_CONFIG_SEED_PATH:-}"
auth_seed_path="${SANDBOX_AUTH_SEED_PATH:-}"
project_name="${SANDBOX_PROJECT_NAME:-builder}"
server_port="${BUILDER_SERVER_PORT:-53082}"
sandbox_home="${SANDBOX_HOME:-/root}"
builder_bin="${BUILDER_SANDBOX_BUILDER_BIN:-/usr/local/bin/builder}"

require_project_create_cli() {
	if HOME="$sandbox_home" "$builder_bin" project create --help >/dev/null 2>&1; then
		return 0
	fi
	echo "sandbox bootstrap requires project registration support. Upgrade Builder so \`builder project create --path <server-path> --name <project-name>\` is available." >&2
	return 1
}

copy_seed_file_if_missing() {
	local target_path="${1:-}"
	local source_path="${2:-}"
	if [ -z "$target_path" ] || [ -z "$source_path" ] || [ ! -f "$source_path" ] || [ -f "$target_path" ]; then
		return 0
	fi
	mkdir -p "$(dirname -- "$target_path")"
	cp "$source_path" "$target_path"
}

wait_for_ready() {
	local deadline=$((SECONDS + 60))
	until curl -fsS "http://127.0.0.1:${server_port}/healthz" >/dev/null 2>&1; do
		if ! kill -0 "$server_pid" >/dev/null 2>&1; then
			return 2
		fi
		if [ "$SECONDS" -ge "$deadline" ]; then
			return 1
		fi
		sleep 1
	done
}

cleanup() {
	if [ -n "${server_pid:-}" ]; then
		kill -TERM "$server_pid" >/dev/null 2>&1 || true
		wait "$server_pid" || true
	fi
}

trap cleanup EXIT INT TERM

mkdir -p "$(dirname -- "$workspace_root")"
mkdir -p "$workspace_root"
mkdir -p "$sandbox_home/.builder"

require_project_create_cli

copy_seed_file_if_missing "$sandbox_home/.builder/config.toml" "$config_seed_path"
copy_seed_file_if_missing "$sandbox_home/.builder/auth.json" "$auth_seed_path"

if [ -z "$(find "$workspace_root" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
	git clone --quiet "$seed_root" "$workspace_root"
fi

cd "$workspace_root"

HOME="$sandbox_home" "$builder_bin" serve --workspace "$workspace_root" "$@" &
server_pid=$!

ready_status=0
wait_for_ready || ready_status=$?
if [ "$ready_status" -ne 0 ]; then
	if [ "$ready_status" -eq 2 ]; then
		wait "$server_pid"
		exit_code=$?
		echo "sandbox bootstrap failed because builder serve exited before transport readiness on 127.0.0.1:${server_port}" >&2
		exit "$exit_code"
	fi
	echo "sandbox bootstrap timed out waiting for builder serve transport readiness on 127.0.0.1:${server_port}" >&2
	exit 1
fi

if ! HOME="$sandbox_home" BUILDER_SERVER_HOST=127.0.0.1 BUILDER_SERVER_PORT="$server_port" "$builder_bin" project "$workspace_root" >/dev/null 2>&1; then
	HOME="$sandbox_home" BUILDER_SERVER_HOST=127.0.0.1 BUILDER_SERVER_PORT="$server_port" "$builder_bin" project create --path "$workspace_root" --name "$project_name"
fi

wait "$server_pid"
