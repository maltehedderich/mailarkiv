# The Linux that `mise run linux:check` and `mise run linux:shell` use on
# macOS: Ubuntu 24.04, as on CI's runners, with the packages CI installs.
FROM ubuntu:24.04@sha256:562111b88caa79c71391c0c4d3c2114a7d5c35915c20561fd893cb1e07929e6b

COPY --from=ghcr.io/jdx/mise:2026.11.14@sha256:7178a0bc7eb30d5090f81237fc0a7830f57e52126b9d8df656e24547437a1e53 /usr/local/bin/mise /usr/local/bin/mise

# build-essential gives the C compiler that `go test -race` needs. strace and
# musl-tools are the packages CI installs.
RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        build-essential ca-certificates curl git musl-tools strace unzip xz-utils \
    && rm -rf /var/lib/apt/lists/*

# Chromium's system libraries, from the Playwright version the frontend locks.
# The tools and packages this needs are removed again.
COPY mise.toml mise.lock /tmp/deps/
COPY frontend/package.json frontend/bun.lock /tmp/deps/frontend/
RUN export MISE_DATA_DIR=/tmp/mise MISE_CACHE_DIR=/tmp/mise/cache MISE_TRUSTED_CONFIG_PATHS=/tmp/deps \
    && cd /tmp/deps/frontend \
    && mise install bun node \
    && mise exec -- bun install --frozen-lockfile --ignore-scripts \
    && mise exec -- bunx playwright install-deps chromium \
    && rm -rf /tmp/deps /tmp/mise /root/.bun /var/lib/apt/lists/*

# The checkout is mounted at /work. These directories become volumes, so
# Linux builds never land in the Mac's checkout, and the volumes start out
# owned by the user the checks run as.
RUN mkdir -p /work/bin /work/frontend/node_modules /work/mailarkiv-search/target \
    && chown -R ubuntu:ubuntu /work \
    && git config --system --add safe.directory /work \
    && echo 'eval "$(mise activate bash)"' >> /etc/bash.bashrc

# Nothing reads the VCS stamp, and git can't follow a worktree's .git file
# out of the mount.
ENV GOFLAGS=-buildvcs=false \
    MISE_TRUSTED_CONFIG_PATHS=/work

# Not root, as on CI's runners.
USER ubuntu
WORKDIR /work
