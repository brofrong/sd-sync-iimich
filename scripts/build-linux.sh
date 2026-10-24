#!/usr/bin/env bash
# Builds the Linux release: the frontend, sd-sync-immich and the AppImage
# that holds it, into <outdir>:
#
#   SD-Sync-Immich-<arch>.AppImage   x86_64 in CI: manual installs and the
#                                    in-app updater download this asset
#
# Usage: scripts/build-linux.sh <version> <outdir>
# The version (e.g. v0.5.0, or dev) is embedded with -ldflags. Set WAILS3
# to use an installed Wails CLI (see scripts/build-frontend.sh), and
# SKIP_FRONTEND=1 to reuse the current frontend/dist.
#
# On Linux it builds natively and needs Go, Node.js, a C compiler,
# pkg-config, libgtk-3-dev and libwebkit2gtk-4.1-dev. The gtk3 build tag
# selects GTK 3 and WebKitGTK 4.1 (CI's ubuntu-22.04 has no WebKitGTK 6);
# building on ubuntu-22.04 makes its glibc (2.35) the oldest the AppImage
# runs with. The AppImage is for the machine's architecture.
#
# Elsewhere (a Mac) it builds the frontend on the host, then runs itself
# in Docker, in ubuntu:22.04 with those packages, like CI. The container
# gets only the repository, read-only, with <outdir> (which must be inside
# it, e.g. dist) writable, plus a named volume for the Go caches.
# BUILD_ARCH=amd64 or arm64 picks the container's architecture (default:
# the host's; amd64 on an Apple Silicon Mac runs emulated, and slowly).
# The AppImage is only built here, never run.
#
# Packaging is Wails' (wails3 generate appimage): it lays out an AppDir
# with the binary, WebKit's helper processes and injected bundle, runs
# linuxdeploy with its GTK plugin (which bundles GTK, WebKitGTK and the
# libraries they need) and packs the AppImage with the type-2 runtime. The
# runtime sets $APPIMAGE and $APPDIR, and Wails' AppRun execs
# usr/bin/sd-sync-immich from inside the mount, which is how the app
# recognises that it runs from its AppImage (internal/install.AppImage).
# linuxdeploy is pinned here and checked by SHA-256: Wails would download
# its "continuous" build. So is the AppImage runtime (the program at the
# start of the AppImage that mounts it), which linuxdeploy's appimage
# plugin would otherwise download from type2-runtime's "continuous"
# release: it is passed in LDAI_RUNTIME_FILE.
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "usage: $0 <version> <outdir>" >&2
	exit 2
fi
version="$1"
root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$2"
outdir="$(cd "$2" && pwd)"

linuxdeploy_release="1-alpha-20251107-1"
runtime_release="20251108"
# linuxdeploy_sha256 <arch> and runtime_sha256 <arch> print the SHA-256 of
# those releases' linuxdeploy-<arch>.AppImage and runtime-<arch> (no
# associative arrays: macOS has bash 3.2).
linuxdeploy_sha256() {
	case "$1" in
		x86_64) echo c20cd71e3a4e3b80c3483cef793cda3f4e990aca14014d23c544ca3ce1270b4d ;;
		aarch64) echo 620095110d693282b8ebeb244a95b5e911cf8f65f76c88b4b47d16ae6346fcff ;;
	esac
}
runtime_sha256() {
	case "$1" in
		x86_64) echo 2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d ;;
		aarch64) echo 00cbdfcf917cc6c0ff6d3347d59e0ca1f7f45a6df1a428a0d6d8a78664d87444 ;;
	esac
}

if [[ "$(uname -s)" != "Linux" ]]; then
	arch="${BUILD_ARCH:-}"
	if [[ -z "$arch" ]]; then
		case "$(uname -m)" in
			arm64 | aarch64) arch=arm64 ;;
			*) arch=amd64 ;;
		esac
	fi
	case "$arch" in
		amd64 | arm64) ;;
		*)
			echo "BUILD_ARCH must be amd64 or arm64, not $arch" >&2
			exit 2
			;;
	esac
	# The container gets only the repository: <outdir> is mounted from
	# inside it.
	case "$outdir/" in
		"$root"/?*) ;;
		*)
			echo "outdir must be inside the repository ($root), e.g. dist" >&2
			exit 2
			;;
	esac
	go_version="$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
	image="sd-sync-immich-linux-build-$arch"
	cache="sd-sync-immich-linux-build-cache-$arch"

	if [[ "${SKIP_FRONTEND:-}" != "1" ]]; then
		"$root/scripts/build-frontend.sh"
	fi

	# The same packages as the release workflow's Linux job, plus what the
	# runner has preinstalled (curl, file, Go).
	docker build --quiet --platform "linux/$arch" --build-arg "GO_VERSION=$go_version" \
		--build-arg "GO_ARCH=$arch" --tag "$image" - >/dev/null <<'DOCKERFILE'
FROM ubuntu:22.04
ARG GO_VERSION
ARG GO_ARCH
RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends \
		ca-certificates curl file build-essential pkg-config \
		libgtk-3-dev libwebkit2gtk-4.1-dev \
	&& rm -rf /var/lib/apt/lists/*
# Checked against the SHA-256 Go's download server publishes for it (the
# one go.dev/dl lists).
RUN go="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
	&& curl -fsSL -o "/tmp/$go" "https://dl.google.com/go/$go" \
	&& echo "$(curl -fsSL "https://dl.google.com/go/$go.sha256")  /tmp/$go" | sha256sum --check --quiet \
	&& tar -xzf "/tmp/$go" -C /usr/local \
	&& rm "/tmp/$go"
ENV PATH=/usr/local/go/bin:$PATH
DOCKERFILE

	docker volume create "$cache" >/dev/null
	docker run --rm --platform "linux/$arch" \
		--volume "$root:/src:ro" \
		--volume "$outdir:/src/${outdir#"$root"/}" \
		--volume "$cache:/cache" \
		--workdir /src \
		--env GOCACHE=/cache/build \
		--env GOMODCACHE=/cache/mod \
		--env GOFLAGS=-buildvcs=false \
		--env SKIP_FRONTEND=1 \
		"$image" \
		/src/scripts/build-linux.sh "$version" "/src/${outdir#"$root"/}"
	exit 0
fi

arch="$(uname -m)"
linuxdeploy_sum="$(linuxdeploy_sha256 "$arch")"
runtime_sum="$(runtime_sha256 "$arch")"
if [[ -z "$linuxdeploy_sum" || -z "$runtime_sum" ]]; then
	echo "unsupported architecture: $arch" >&2
	exit 1
fi
# The Wails CLI links Wails' GTK code too: it needs the tag as well, also
# for the bindings scripts/build-frontend.sh generates.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=gtk3"
wails3="${WAILS3:-go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.34}"
appimage="SD-Sync-Immich-$arch.AppImage"

if [[ "${SKIP_FRONTEND:-}" != "1" ]]; then
	"$root/scripts/build-frontend.sh"
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cd "$root"
ldflags="-s -w -X github.com/dmitiigorshkov/sd-sync-immich/internal/version.Current=$version"
# -tags production: without it Wails runs in development mode (web
# inspector, dev-server asset handling). gtk3: see above.
CGO_ENABLED=1 go build -tags production,gtk3 -ldflags "$ldflags" \
	-o "$work/sd-sync-immich" ./cmd/sd-sync-immich

# Wails uses the linuxdeploy it finds in the build directory.
mkdir -p "$work/appimage"
linuxdeploy="$work/appimage/linuxdeploy-$arch.AppImage"
curl -fsSL --retry 3 -o "$linuxdeploy" \
	"https://github.com/linuxdeploy/linuxdeploy/releases/download/$linuxdeploy_release/linuxdeploy-$arch.AppImage"
echo "$linuxdeploy_sum  $linuxdeploy" | sha256sum --check --quiet
chmod 0755 "$linuxdeploy"
runtime="$work/runtime-$arch"
curl -fsSL --retry 3 -o "$runtime" \
	"https://github.com/AppImage/type2-runtime/releases/download/$runtime_release/runtime-$arch"
echo "$runtime_sum  $runtime" | sha256sum --check --quiet
export LDAI_RUNTIME_FILE="$runtime"

# shellcheck disable=SC2086 # $wails3 may be a command with arguments.
$wails3 generate appimage \
	-binary "$work/sd-sync-immich" \
	-icon "$root/build/linux/sd-sync-immich.png" \
	-desktopfile "$root/build/linux/sd-sync-immich.desktop" \
	-builddir "$work/appimage" \
	-outputdir "$work"

mv "$work/sd-sync-immich-$arch.AppImage" "$outdir/$appimage"
chmod 0755 "$outdir/$appimage"
echo "Built $version into $outdir/$appimage"
