# App-Server Migration Blockers

Related note:

- Sandbox helper usage and caveats: `docs/dev/builder-serve-sandbox.md`

## Resolved Historical Blocker: Remote Sandbox Attachment And Pathless Startup

Status: resolved in code for the current shipping TUI/headless boundary

Resolved contract:

- remote attach no longer depends on host-local metadata bindings to discover the target project/workspace
- the client can connect unscoped, query server-owned project/workspace state, and then attach explicitly
- remote attach is resilient to host/server path mismatch through explicit server-owned `workspace_id` selection when path hints are insufficient
- startup now splits into:
  - local-path mode when the server can resolve the client's requested path
  - server-browsing mode when it cannot
- in server-browsing mode, the client opens existing server projects/workspaces only and does not offer bind/create for the client path
- first setup for server-browsing mode is handled through RPC-first admin commands against the running daemon:
  - `builder project list`
  - `builder project create --path <server-path> --name <project-name>`
  - `builder attach --project <project-id> <server-path>`
- multi-workspace remote attach now uses explicit server-owned `workspace_id` rather than implicit server defaulting

Remaining narrower follow-up:

- a fully generic pathless/non-TUI frontend attach UX may still want a more explicit unscoped browse/select protocol, but that is no longer a release blocker for the current TUI/headless shipping path

## Resolved Historical Blocker: Remote Server Auth Bootstrap Requires Server-First Auth

Status: resolved in code

Resolved contract:

- `builder serve` can now start unauthenticated and still expose `/rpc`, `/healthz`, and `/readyz`
- transport exposes explicit pre-auth auth bootstrap methods: `auth.getBootstrapStatus` and `auth.completeBootstrap`
- after `protocol.handshake`, clients can discover `auth_ready`, `auth_bootstrap_supported`, `allowed_pre_auth_methods`, and supported bootstrap modes without attaching first
- gateway rejects auth-dependent operations with explicit auth-required errors instead of failing startup before transport exists
- remote auth bootstrap is server-owned: client collects browser/device/paste/api-key material, server persists the resulting auth method
- remote app-server client code no longer treats host-local `~/.builder/auth.json` as the remote server auth source of truth
