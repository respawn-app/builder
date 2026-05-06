---
title: Sandboxing and Security
description: Builder's default trust model, outside-workspace edit prompts, and remote/container server setup.
---

Builder is YOLO by default: it does not run tools inside a built-in sandbox.
The agent executes shell commands and file tools in the environment where the Builder server runs.
If that environment can read secrets, reach networks, or modify files, the agent can ask tools to do the same.

However, Builder's [client-server](../server/) architecture makes it easy to run Builder in a **completely isolated, secure container or VM**.

## Outside-Workspace Edits

Builder has a small convenience guard for first-class file edit tools.
By default, native edit tools prompt before modifying files outside the session workspace root. To disable, set config:

```toml
allow_non_cwd_edits = true
```

This is not sandboxing: the agent can easily bypass this with shell. It's intended for hallucination and erroneous/mismatched CWD prevention.

## Server Boundary

Builder separates frontend clients from the server that owns work.
The terminal UI, headless runs, and other surfaces connect to the configured server when one is available.
The server owns sessions, project/workspace bindings, shell processes, tool execution, and persistence.

That split makes the server environment the useful security boundary:

- Run `builder serve` on a VM and connect from your laptop.
- Run `builder serve` in Docker and expose only the Builder port.
- Run several isolated servers on different ports for different trust zones.

Paths are resolved on the server.
When you create or attach a project against a remote/container server, the workspace path must exist inside that server environment, not on the client machine.

## Container Image Shape

A Builder sandbox image should contain:

- A `builder` binary compatible with the client version you use.
- Runtime tools the agent may need: shell, Git, language toolchains, package managers, `rg`, `fd`, `jq`, `patch`, `curl`, `gh`, `wget`, `python` and project-specific CLIs.
- An ideally persistent workspace directory such as `/workspace`.
- A writable Builder persistence root, usually under the sandbox user's home.
- Credentials mounted or injected only when you intend the sandbox to use them.
- Network policy that matches the task; disable or restrict egress when needed.

Avoid mounting your host home directory or broad source trees into the sandbox.
Mount only the workspace, caches, and credentials the task needs.

## Example Dockerfile

This is a generic starting point.
Add the language runtimes and project tools your workflows need.

```dockerfile
FROM debian:bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/home/builder
ENV SHELL=/bin/bash
ARG BUILDER_VERSION=

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    fd-find \
    file \
    git \
    jq \
    less \
    netcat-openbsd \
    openssh-client \
    patch \
    procps \
    python3 \
    python3-pip \
    python3-venv \
    ripgrep \
    tar \
    tini \
    trash-cli \
    unzip \
    xz-utils \
    zip \
  && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
  && useradd --create-home --shell /bin/bash builder \
  && mkdir -p /workspace /home/builder/.builder \
  && chown -R builder:builder /workspace /home/builder

SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl -fsSL https://raw.githubusercontent.com/respawn-app/builder/main/scripts/install.sh \
  | BUILDER_PREFIX=/usr/local BUILDER_VERSION="${BUILDER_VERSION}" sh

USER builder
WORKDIR /workspace
EXPOSE 53082

ENTRYPOINT ["tini", "--"]
CMD ["builder", "serve"]
```

The image installs the latest release by default.
Build with `docker build --build-arg BUILDER_VERSION=vX.Y.Z -t builder-sandbox .` if you need to pin one Builder release.
Package-manager cache cleanup is useful for smaller images but omitted here for clarity.

Run the server so it listens inside the container and is reachable from the host:

```bash
docker run --name builder-sandbox --rm -it \
  -p 127.0.0.1:53082:53082 \
  -e BUILDER_SERVER_HOST=0.0.0.0 \
  -e BUILDER_SERVER_PORT=53082 \
  -v "$PWD:/workspace" \
  builder-sandbox
```

In another terminal, point the local client at that server:

```bash
BUILDER_SERVER_HOST=127.0.0.1 BUILDER_SERVER_PORT=53082 builder project create --path /workspace --name sandbox
BUILDER_SERVER_HOST=127.0.0.1 BUILDER_SERVER_PORT=53082 builder
```

The project path is `/workspace` because that is the path visible to the server.

## Existing Repository Example

The repository includes a Docker example under `scripts/sandbox`.
Treat it as a Builder development fixture, not a recommended user image.
It copies this repository into the image, seeds a workspace, and starts `builder serve`.
Use it to understand one possible entrypoint shape, then build an image for your own toolchain and isolation policy.
