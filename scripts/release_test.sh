#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release_script="$script_dir/release.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export GIT_AUTHOR_NAME="Release Test"
export GIT_AUTHOR_EMAIL="release-test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"

git init --bare --quiet "$tmp/remote.git"
git clone --quiet "$tmp/remote.git" "$tmp/work"
cd "$tmp/work"
printf 'initial\n' > README.md
git add README.md
git commit --quiet -m "initial"
git branch -M main
git push --quiet -u origin main
git tag v1.2.3
git push --quiet origin v1.2.3
printf 'feature\n' >> README.md
git add README.md
git commit --quiet -m "add feature"
git push --quiet origin main

output="$(RELEASE_CONFIRM=yes RELEASE_DRY_RUN=1 "$release_script" minor)"
if [[ "$output" != *"v1.2.3 → v1.3.0"* ]]; then
	echo "expected minor bump summary, got: $output" >&2
	exit 1
fi
if git rev-parse --verify --quiet refs/tags/v1.3.0 >/dev/null; then
	echo "dry run unexpectedly created v1.3.0" >&2
	exit 1
fi

printf 'dirty\n' > dirty.txt
if RELEASE_CONFIRM=yes RELEASE_DRY_RUN=1 "$release_script" patch >"$tmp/dirty.out" 2>&1; then
	echo "release succeeded with a dirty working tree" >&2
	exit 1
fi
if ! command rg --quiet "незакоммиченные изменения" "$tmp/dirty.out"; then
	echo "dirty-tree error was not reported" >&2
	exit 1
fi
rm dirty.txt

RELEASE_CONFIRM=yes "$release_script" patch >"$tmp/release.out"
if ! git ls-remote --exit-code --tags origin refs/tags/v1.2.4 >/dev/null; then
	echo "release did not push v1.2.4" >&2
	exit 1
fi

if RELEASE_CONFIRM=yes RELEASE_DRY_RUN=1 "$release_script" patch >"$tmp/empty.out" 2>&1; then
	echo "release succeeded without commits after the latest tag" >&2
	exit 1
fi
if ! command rg --quiet "нет новых коммитов" "$tmp/empty.out"; then
	echo "empty-release error was not reported" >&2
	exit 1
fi

echo "release script tests passed"
