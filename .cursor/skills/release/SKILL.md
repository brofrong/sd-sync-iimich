---
name: release
description: Analyzes repository changes, recommends a SemVer major, minor, or patch bump, presents release notes for approval, and publishes an approved Git tag. Use when the user asks to release, publish a version, create a tag, or bump the application version.
---

# Release

Create releases only from a clean, synchronized `main` branch. GitHub Actions builds the release after the new tag is pushed.

## Workflow

1. Inspect without changing repository state:
   - `git status --short --branch`
   - latest SemVer tag matching `v*`
   - `git log <latest-tag>..HEAD --oneline`
   - `git diff <latest-tag>..HEAD`
   - relationship between `main` and `origin/main`
2. Stop if:
   - the working tree contains tracked or untracked changes;
   - the current branch is not `main`;
   - local `main` differs from `origin/main`;
   - there are no commits after the latest tag.
3. Classify every change and select the highest required bump:
   - **major**: an intentional incompatible change to CLI, configuration, persisted data, integration behavior, or supported API;
   - **minor**: a backward-compatible user-visible feature;
   - **patch**: a bug fix, documentation, tests, CI, refactoring, dependency maintenance, or internal improvement.
4. Treat a breaking change as `major` only when the incompatibility is demonstrated by the diff or explicitly stated. Do not infer `major` merely from a large diff.
5. Present the proposal before any mutation:

```text
Current version: vX.Y.Z
Recommended bump: minor
Proposed version: vX.(Y+1).0

Reason:
- ...

Release notes:
- ...
```

6. Ask the user to choose `major`, `minor`, `patch`, or cancel. Recommend the classification from step 3, but allow an explicit override.
7. Only after explicit confirmation, run:

```bash
RELEASE_CONFIRM=yes scripts/release.sh <confirmed-bump>
```

8. Report the pushed tag and GitHub release URL. Explain that the release may remain in progress while GitHub Actions builds its assets.

## Safety

- Never create commits, edit files, push a branch, or clean the working tree as part of this skill.
- Never bypass checks in `scripts/release.sh`.
- Never create or push a tag before the user approves the exact version.
- If the script fails, report the exact failure. Do not manually recreate or force-push the tag.
