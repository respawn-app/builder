#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go test ./shared/rpcwire -run '^$' -bench 'Benchmark(TransportRoundTripTCP|TransportRoundTripTCPGWS|TransportRoundTripLocalSocket|TransportRoundTripLocalSocketGWS|TransportDialTCP|TransportDialTCPGWS|TransportDialLocalSocket|TransportDialLocalSocketGWS)$' -benchtime=300x "$@"

go test ./shared/client -run '^$' -bench 'Benchmark(RemoteGetSessionMainViewPersistent|RemoteGetSessionMainViewPersistentGWS|RemoteGetSessionTranscriptPagePersistent|RemoteGetSessionTranscriptPagePersistentGWS|DialRemoteAttachProjectWorkspace|DialRemoteAttachProjectWorkspaceGWS|RemoteGetSessionMainViewPersistentLocalSocket|RemoteGetSessionMainViewPersistentLocalSocketGWS|RemoteGetSessionTranscriptPagePersistentLocalSocket|RemoteGetSessionTranscriptPagePersistentLocalSocketGWS|DialHandshakeLocalSocket|DialHandshakeLocalSocketGWS|AttachProjectWorkspaceLocalSocket|AttachProjectWorkspaceLocalSocketGWS)$' -benchtime=300x "$@"
