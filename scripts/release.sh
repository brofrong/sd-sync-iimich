#!/usr/bin/env bash
set -euo pipefail

die() {
	echo "release: $*" >&2
	exit 1
}

usage() {
	echo "Usage: scripts/release.sh <major|minor|patch|bugfix>" >&2
	exit 2
}

[[ $# -eq 1 ]] || usage
bump="$1"
[[ "$bump" == "bugfix" ]] && bump="patch"
case "$bump" in
	major|minor|patch) ;;
	*) usage ;;
esac

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "запускайте скрипт внутри Git-репозитория"
root="$(git rev-parse --show-toplevel)"
cd "$root"

branch="$(git symbolic-ref --quiet --short HEAD || true)"
[[ "$branch" == "main" ]] || die "релиз разрешён только из ветки main (сейчас: ${branch:-detached HEAD})"
[[ -z "$(git status --porcelain)" ]] || die "есть незакоммиченные изменения"

git remote get-url origin >/dev/null 2>&1 || die "remote origin не настроен"
git fetch --quiet origin main --tags
local_head="$(git rev-parse HEAD)"
remote_head="$(git rev-parse refs/remotes/origin/main)"
[[ "$local_head" == "$remote_head" ]] || die "main должна точно совпадать с origin/main"

latest=""
while IFS= read -r tag; do
	latest="$tag"
	break
done < <(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname)
[[ -n "$latest" ]] || latest="v0.0.0"

if [[ ! "$latest" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
	die "последний тег $latest не соответствует SemVer vMAJOR.MINOR.PATCH"
fi
if git rev-parse --verify --quiet "$latest^{commit}" >/dev/null &&
	[[ "$(git rev-list --count "$latest..HEAD")" -eq 0 ]]; then
	die "после $latest нет новых коммитов"
fi
major="${BASH_REMATCH[1]}"
minor="${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"

case "$bump" in
	major)
		major=$((major + 1))
		minor=0
		patch=0
		;;
	minor)
		minor=$((minor + 1))
		patch=0
		;;
	patch)
		patch=$((patch + 1))
		;;
esac
next="v${major}.${minor}.${patch}"

echo "Релиз: $latest → $next"
echo "Ветка: $branch"
echo "Коммиты:"
if git rev-parse --verify --quiet "$latest^{commit}" >/dev/null; then
	git log --oneline "$latest..HEAD"
else
	git log --oneline
fi

if [[ "${RELEASE_CONFIRM:-}" != "yes" ]]; then
	read -r -p "Создать и отправить тег $next? [y/N] " answer
	[[ "$answer" == "y" || "$answer" == "Y" ]] || die "отменено пользователем"
fi

if [[ "${RELEASE_DRY_RUN:-}" == "1" ]]; then
	echo "Dry run: тег не создан и не отправлен."
	exit 0
fi

git tag -a "$next" -m "Release $next"
if ! git push origin "$next"; then
	git tag -d "$next" >/dev/null
	die "push не удался; локальный тег $next удалён"
fi
echo "Тег $next отправлен. GitHub Actions создаст релиз."
