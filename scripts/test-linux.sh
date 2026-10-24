#!/usr/bin/env bash
# Runs `go vet ./...` and `go test ./...` for Linux in Docker, the way CI's
# ubuntu job does, from a Mac (or any machine with Docker).
#
# The frontend is built on the host first (scripts/build-frontend.sh), as
# the Go binary embeds frontend/dist: building it in the container would
# replace the host's node_modules with Linux binaries. Set SKIP_FRONTEND=1
# to reuse the current frontend/dist.
#
# The container gets only the repository, read-only, plus a named volume
# for the Go module and build caches, so repeated runs are fast. The image
# (golang:1.26-bookworm with the GTK and WebKitGTK headers Wails needs) is
# built once and cached by Docker. Wails builds against GTK 4 unless the
# gtk3 build tag selects GTK 3 and WebKitGTK 4.1, which the app uses (as
# CI's ubuntu-22.04 has no WebKitGTK 6). The tests run as an unprivileged
# user: root would skip the tests that need file permissions to apply.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
image="sd-sync-immich-linux-test"
cache="sd-sync-immich-linux-go-cache"

if [[ "${SKIP_FRONTEND:-}" != "1" ]]; then
	"$root/scripts/build-frontend.sh"
fi

docker build --quiet --tag "$image" - >/dev/null <<'DOCKERFILE'
FROM golang:1.26-bookworm
RUN apt-get update \
	&& apt-get install --yes --no-install-recommends libgtk-3-dev libwebkit2gtk-4.1-dev \
	&& rm -rf /var/lib/apt/lists/*
RUN useradd --create-home --uid 1000 builder
DOCKERFILE

docker volume create "$cache" >/dev/null

docker run --rm \
	--volume "$root:/src:ro" \
	--volume "$cache:/cache" \
	--workdir /src \
	--env GOCACHE=/cache/build \
	--env GOMODCACHE=/cache/mod \
	--env "GOFLAGS=-buildvcs=false -tags=gtk3" \
	"$image" \
	bash -c 'chown builder /cache && exec runuser -u builder -- bash -c "set -e; go version; go vet ./...; go test ./..."'
