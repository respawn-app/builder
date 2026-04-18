# Builder Serve Sandbox

`scripts/sandbox-serve.sh` builds a Docker image that compiles `builder` from the same copied repo snapshot it ships, seeds only host `config.toml` and `auth.json` into an isolated container home on first boot, clones a sandboxed Builder repo at the container workspace path, registers project `builder`, then runs `builder serve` in the container.

Properties:

- Container listens on `0.0.0.0` and exposes `/rpc`, `/healthz`, `/readyz` to the host.
- Only host `~/.builder/config.toml` and `~/.builder/auth.json` are seeded by default; the rest of the host home stays isolated.
- Those seed files are copied only if missing in the sandbox home volume. `scripts/sandbox-serve.sh down --reset` drops sandbox state and re-seeds on next boot.
- Builder persistence lives in a separate named Docker volume mounted at `/root`, so sandbox auth/session state diverges after first boot and becomes sandbox-owned.
- The workspace defaults to `/workspace/builder` inside the container. Builder no longer needs the host repo path mirrored into the sandbox.
- First boot registers the cloned workspace as server project `builder` via the documented server-admin CLI surface.

Bootstrap order:

1. `scripts/sandbox-serve.sh up` builds the Docker image.
2. The image build compiles `builder` from the copied repo snapshot.
3. Container entrypoint copies seeded `config.toml` and `auth.json` into sandbox home if missing.
4. Entry-point clones the baked repo seed into the writable container workspace path if that workspace volume is empty.
5. Entry-point starts `builder serve`, waits for `/healthz`, then registers the workspace with `builder project create --path ... --name ...` if needed.

Container startup never compiles the binary itself.

Example:

```bash
scripts/sandbox-serve.sh up --host-port 53100 --project-name builder -- --model gpt-5.4
eval "$(scripts/sandbox-serve.sh env --host-port 53100)"
cd /Users/nek/Developer/builder-cli
builder
```

The local frontend can run from the normal host checkout while the server workspace lives at `/workspace/builder` inside the container.

Known caveat:

- Runtime work done inside the sandbox changes the container checkout, not the host checkout. Rebuild/restart `builder serve` inside the sandbox when you need the running server binary to pick up sandbox-side source edits.
