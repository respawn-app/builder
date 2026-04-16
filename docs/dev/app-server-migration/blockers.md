# App-Server Migration Blockers

Related note:

- Sandbox helper usage and caveats: `docs/dev/builder-serve-sandbox.md`

## Remote Server Auth Bootstrap Requires Server-First Auth

Status: open

Needed direction:

- `1.` Server must be able to boot unauthenticated and expose auth/bootstrap RPC so a client can complete auth after transport connect.
- `2.` The auth/bootstrap contract must be explicit and discoverable on an unscoped connection:
  - after `protocol.handshake`, client must be able to learn whether auth is ready and whether remote auth bootstrap is supported
  - planned contract is either handshake status fields or guaranteed-pre-auth `auth.getBootstrapStatus`
  - the discoverability payload must include at least `auth_ready`, `auth_bootstrap_supported`, and `allowed_pre_auth_methods`
- `3.` Remote browser/device auth cannot reuse the existing localhost callback flow unchanged across machines.
  - client-side UX still drives browser/device/paste interaction on the client machine
  - client collects callback URL, authorization code, or equivalent auth material locally
  - client sends that material to the server over planned `auth.completeBootstrap`
  - server performs any required exchange and writes the resulting auth method into the server auth store

Current constraint:

- `builder serve` uses headless startup handlers and enforces auth readiness during startup.
- When no auth material exists in the server persistence/env, startup fails before `/rpc` is available.
- Because the server never reaches transport readiness, a local client cannot connect and drive auth for that server.

Impact:

- A Dockerized or remote `builder serve` instance cannot be cold-started from a clean sandbox and then authenticated by the host client.
- True “client authenticates server” flow is blocked on allowing server boot before auth is configured.

Implication for sandbox work:

- Fresh sandbox startup still needs pre-seeded auth material today (`OPENAI_API_KEY`, saved auth in sandbox persistence, or explicit OpenAI-compatible config).

Planned fix direction:

- do not overload `project.attach` for this flow
- use separate auth/bootstrap RPC before project/session attach

## Remote Sandbox Attachment Still Depends On Host-Side Binding Metadata

Status: open

Constraint:

- Local interactive/headless client startup still resolves the active project from the host persistence root before it will attach to a remote server.
- `cli/app/run_prompt_target.go` and `cli/app/session_server_target.go` both go through `loadRemoteAttachState(...)`, which reads local config plus local metadata bindings.
- Remote dialing then requires a non-empty `project_id` in the initial `project.attach` handshake.
- Current project APIs do not yet expose explicit workspace enumeration/selection for remote attach, so host path lookup is still standing in for server-owned workspace identity.

Impact:

- A Dockerized `builder serve` can expose `/rpc` and can mirror the same workspace path, but transparent host-client attach still fails when container persistence is fully isolated from host persistence.
- Today, a true “different machine” sandbox needs either shared persistence/bindings, a manual remote attach flow that does not require prior local binding metadata, or protocol/client changes.
- For projects with multiple workspaces/worktrees, any implicit server-chosen workspace default would be ambiguous and unsafe.

Evidence gathered while implementing the Docker sandbox helper:

- `shared/client/remote.go` always sends `project.attach` during dial.
- `shared/protocol/handshake.go` requires `project_id` in `AttachProjectRequest`.
- `server/transport/gateway.go` resolves attached workspace roots against server-side metadata, so path identity matters too.

Planned fix direction:

- allow unscoped dial after `protocol.handshake` without immediate `project.attach`
- keep `project.attach` as an explicit scoped attach once the client has chosen a server-owned target
- extend project query surfaces so the client can enumerate/select server-owned workspaces before scoped attach when path hints are insufficient
- the explicit selection identity for multi-workspace remote attach is expected to be `workspace_id`, not fuzzy defaulting or host-path equality
