FROM golang:1.25.0-bookworm

ARG SANDBOX_SNAPSHOT_REF=local

ENV DEBIAN_FRONTEND=noninteractive
ENV HOME=/root
ENV SHELL=/bin/bash
ENV SANDBOX_SEED_ROOT=/opt/builder-sandbox-seed
ENV PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		curl \
		dnsutils \
		fd-find \
		file \
		fzf \
		gh \
		git \
		iproute2 \
		jq \
		less \
		lsof \
		netcat-openbsd \
		openssh-client \
		patch \
		python3-pip \
		python3-venv \
		procps \
		python3 \
		ripgrep \
		rsync \
		sqlite3 \
		strace \
		tini \
		tmux \
		tree \
		trash-cli \
		unzip \
		xz-utils \
		yq \
		zip \
		zsh \
	&& python3 -m pip install --break-system-packages --no-cache-dir uv \
	&& ln -sf /usr/bin/fdfind /usr/local/bin/fd \
	&& ln -sf /usr/bin/pip3 /usr/local/bin/pip \
	&& ln -sf /usr/bin/python3 /usr/local/bin/python \
	&& ln -sf /usr/local/go/bin/go /usr/local/bin/go \
	&& ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
	&& printf 'export PATH=/go/bin:/usr/local/go/bin:$PATH\n' >/etc/profile.d/go-path.sh

WORKDIR /opt/builder-sandbox-seed

COPY . /opt/builder-sandbox-seed
COPY scripts/sandbox/builder-sandbox-entrypoint.sh /usr/local/bin/builder-sandbox-entrypoint

RUN /opt/builder-sandbox-seed/scripts/build.sh --output /usr/local/bin/builder \
	&& chmod +x /usr/local/bin/builder /usr/local/bin/builder-sandbox-entrypoint \
	&& git -C /opt/builder-sandbox-seed init -q \
	&& git -C /opt/builder-sandbox-seed config user.name "Builder Sandbox" \
	&& git -C /opt/builder-sandbox-seed config user.email "builder-sandbox@local" \
	&& git -C /opt/builder-sandbox-seed add -A \
	&& git -C /opt/builder-sandbox-seed commit -qm "chore: sandbox seed ${SANDBOX_SNAPSHOT_REF}"

ENTRYPOINT ["tini", "--", "/usr/local/bin/builder-sandbox-entrypoint"]
