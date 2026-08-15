# Automatic Updates and Release Automation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add automatic and manual update checks backed by tag-only GitHub releases, plus a guarded semantic-version release workflow.

**Architecture:** A testable `internal/update` package owns GitHub release discovery, semantic-version comparison, checksum verification, and atomic binary replacement. The tray controller runs checks asynchronously, exposes a manual menu action, asks before installation, then exits so the existing `KeepAlive` LaunchAgent restarts the updated binary. A project release skill analyzes changes and delegates the final tag creation and push to a defensive shell script.

**Tech Stack:** Go standard library, GitHub Releases REST API, macOS AppleScript dialogs, Bash, GitHub Actions.

---

### Task 1: Version and release discovery

**Files:**
- Create: `internal/version/version.go`
- Create: `internal/update/checker.go`
- Test: `internal/update/checker_test.go`

1. Write failing table tests for semantic-version comparison, development builds, GitHub API responses, and missing release assets.
2. Run `go test ./internal/update` and confirm failure because the API is absent.
3. Add the minimal checker with an injected HTTP client and repository URL.
4. Run package tests and keep them green.

### Task 2: Verified atomic installation

**Files:**
- Create: `internal/update/installer.go`
- Test: `internal/update/installer_test.go`

1. Write failing tests for checksum parsing, checksum mismatch, executable permissions, and atomic replacement.
2. Run package tests and confirm expected failures.
3. Implement architecture-specific asset selection, bounded downloads, SHA-256 verification, temporary-file writes, and atomic rename.
4. Run package tests and keep them green.

### Task 3: Menu bar integration

**Files:**
- Modify: `internal/tray/tray_darwin.go`
- Modify: `internal/settings/dialog_darwin.go`
- Modify: `cmd/sd-sync-immich/main.go`

1. Add reusable dialogs for update results and installation confirmation.
2. Inject the updater into the tray controller.
3. Add `Проверить обновления…`, an update-available menu item, startup checking, and a 12-hour timer.
4. Keep network and download work off the systray callback path.
5. After successful replacement, quit so LaunchAgent restarts the binary.

### Task 4: Tag-only GitHub releases

**Files:**
- Modify: `.github/workflows/release.yml`
- Create: `scripts/release.sh`
- Test: `scripts/release_test.sh`

1. Remove manual workflow dispatch so release builds only run for pushed `v*` tags.
2. Pass the tag into the binary through linker flags.
3. Test release-script bump calculation and refusal conditions with temporary Git repositories.
4. Implement a script that requires a clean, up-to-date `main`, computes the next SemVer tag, shows a summary, requires explicit confirmation, and pushes only the new tag.

### Task 5: Project release skill and documentation

**Files:**
- Create: `.cursor/skills/release/SKILL.md`
- Modify: `README.md`

1. Define bump classification: breaking change → major, feature → minor, fixes/docs/internal changes → patch.
2. Require analysis since the latest tag and present current version, proposed version, rationale, and release notes.
3. Require user confirmation before executing `scripts/release.sh`.
4. Document automatic/manual update behavior and the tag-only release process.

### Task 6: Verification

1. Run `gofmt` on changed Go files.
2. Run updater package tests and release-script tests.
3. Run `go test ./...`, recording the pre-existing config test separately if it remains unresolved.
4. Run `go vet ./...` and build with a test linker-injected version.
5. Validate clean diffs and ensure no tag, commit, or push was created.
