#!/usr/bin/env bash
# Builds the Svelte frontend into frontend/dist, which the Go binary embeds
# (see frontend/embed.go): installs npm dependencies from the lockfile,
# generates the TypeScript bindings for the Go services and events into
# frontend/bindings, then runs the Vite build. `npm run build` recreates
# frontend/dist/.gitkeep, which Vite deletes when it empties dist.
#
# The Wails CLI is pinned to the Wails version in go.mod and run through
# `go run`, so no global install is needed; set WAILS3 to use another
# binary (e.g. WAILS3=wails3).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
wails3="${WAILS3:-go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34}"

cd "$root/frontend"
npm ci --no-audit --no-fund

cd "$root"
# shellcheck disable=SC2086 # $wails3 may be a command with arguments.
$wails3 generate bindings -silent -clean=true -d frontend/bindings -ts ./cmd/sd-sync-immich

cd "$root/frontend"
npm run build
