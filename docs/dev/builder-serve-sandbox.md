# Builder Serve Sandbox

`scripts/sandbox-serve.sh` builds a Linux Builder binary, bakes it plus a clean snapshot of this repo into a Docker image, then runs `builder serve` in an isolated container.

Properties:

- Container listens on `0.0.0.0` and exposes `/rpc`, `/healthz`, `/readyz` to the host.
- Repo snapshot is seeded into a named Docker volume at the same absolute workspace path as the host repo. That preserves deterministic workspace/project identity based on canonical path.
- Builder persistence lives in a separate named Docker volume mounted at `/root`, so auth/session state stays isolated from the host machine.
- Common auth-related env vars are forwarded narrowly: `OPENAI_API_KEY`, `BUILDER_OAUTH_CLIENT_ID`, `BUILDER_PROVIDER_OVERRIDE`, `BUILDER_OPENAI_BASE_URL`.
- Fresh sandbox startup still needs auth material in the container context: forwarded `OPENAI_API_KEY`, saved auth in the sandbox home volume, or an explicit OpenAI-compatible config passed through `builder serve` args.
- `scripts/sandbox-serve.sh up` now checks that auth/config precondition before building the image or launching the container.

Example:

```bash
scripts/sandbox-serve.sh up --host-port 53100 -- --model gpt-5.4
eval "$(scripts/sandbox-serve.sh env --host-port 53100)"
```

Known caveat:

- Current remote client attach flow still depends on local metadata/project bindings. This sandbox script solves network exposure and workspace-path identity, but it does not remove that host-vs-container metadata coupling.
