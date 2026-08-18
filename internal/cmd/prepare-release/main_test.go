package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateDraftRelease(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/release\n", nil
	})

	releaseURL, err := createDraftRelease(eff, "/worktree", "1.2.3", "Notes")
	if err != nil {
		t.Fatal(err)
	}
	if releaseURL != "https://example.com/release" {
		t.Fatalf("unexpected release URL: %q", releaseURL)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh release create v1.2.3 --draft --title v1.2.3 --notes-file /worktree/prepare-release-notes.md --generate-notes
	`)
	testEqual(t, eff.files["/worktree/prepare-release-notes.md"], "Notes")
}

func TestOpenDraftPR(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/pr\n", nil
	})

	prURL, err := openDraftPR(eff, "/worktree", "release", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if prURL != "https://example.com/pr" {
		t.Fatalf("unexpected PR URL: %q", prURL)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh pr create --draft --base main --head release --title "Prepare release 1.2.3" --body "Prepare Go SDK release 1.2.3."
	`)
}

func TestPushBranch(t *testing.T) {
	eff := newMockEffects(nil)

	err := pushBranch(eff, "/worktree", "release")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git push --set-upstream origin release
	`)
}

func TestCommitRelease(t *testing.T) {
	eff := newMockEffects(nil)

	err := commitRelease(eff, "/worktree", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare release 1.2.3" -- CHANGELOG.md internal/version.go
	`)
}

func TestCreateDraftPRStopsBeforePush(t *testing.T) {
	eff := newMockEffects(nil)

	_, err := createDraftPR(eff, "/worktree", "release", "1.2.3", true)
	if err == nil || !strings.Contains(err.Error(), "--stop-before-push") {
		t.Fatalf("expected stop-before-push error, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare release 1.2.3" -- CHANGELOG.md internal/version.go
	`)
}

func TestUpdateFile(t *testing.T) {
	eff := newMockEffects(nil)
	eff.files["version.txt"] = "old"

	updated, err := updateFile(eff, "version.txt", func(contents string) (string, error) {
		return contents + "-new", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated != "old-new" || eff.files["version.txt"] != "old-new" {
		t.Fatalf("unexpected updated file: returned %q, stored %q", updated, eff.files["version.txt"])
	}
}

func TestCreateWorktree(t *testing.T) {
	eff := newMockEffects(func(cmd command) (string, error) {
		if cmd.String() == `git log -1 "--format=%s (%h)"` {
			return "Initial commit (abc123)\n", nil
		}
		return "", nil
	})

	root, cleanup, err := createWorktree(eff, "/foo/bar", "quux")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/foo/bar: git worktree add -b quux /tmp/prepare-go-release-123456 origin/main
		/tmp/prepare-go-release-123456: git log -1 "--format=%s (%h)"
	`)
	testEqual(t, eff.output.String(), `
		      $ git worktree add -b quux /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
	`)
	if root != eff.tempDir {
		t.Fatalf("unexpected worktree root: got %q, want %q", root, eff.tempDir)
	}

	err = cleanup()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/foo/bar: git worktree add -b quux /tmp/prepare-go-release-123456 origin/main
		/tmp/prepare-go-release-123456: git log -1 "--format=%s (%h)"
		/foo/bar: git worktree remove --force /tmp/prepare-go-release-123456
	`)
}

func TestFetchMain(t *testing.T) {
	eff := newMockEffects(nil)

	err := fetchMain(eff, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `/repo: git fetch origin main`)
}

func TestValidateReleaseFilesRejectsNonIncreasingVersion(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.47.0"
		)
	`)

	eff := newMockEffects(nil)

	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := validateReleaseFiles(eff, eff.tempDir, "1.47.0")
	if err == nil || !strings.Contains(err.Error(), "must increment") {
		t.Fatalf("expected version increase error, got %v", err)
	}
	testEqual(t, eff.output.String(), "")
}

func TestValidateReleaseFilesRejectsInvalidChangelog(t *testing.T) {
	eff := newMockEffects(nil)
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\nrequire go.temporal.io/api v1.63.4\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.47.0"`
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = "## [Unreleased]\n\n## [1.47.0]\n\n## [1.46.0]\n\n## [1.46.0]\n"

	err := validateReleaseFiles(eff, eff.tempDir, "1.48.0")
	if err == nil || !strings.Contains(err.Error(), `found 2 sections for "1.46.0"`) {
		t.Fatalf("expected duplicate changelog section error, got %v", err)
	}
}

func TestPrepareDraftPRValidatesBeforeCreatingBranch(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578
	`)

	eff := newMockEffects(func(cmd command) (string, error) {
		if cmd.String() == `git log -1 "--format=%s (%h)"` {
			return "Initial commit (abc123)\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod

	err := prepareEverything(eff, "1.48.0", time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC), false)
	if err == nil || !strings.Contains(err.Error(), "must use an official release") {
		t.Fatalf("expected API version validation failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Worktree preserved at "+eff.tempDir) ||
		!strings.Contains(err.Error(), "git branch --delete --force chore/release-1.48.0") {
		t.Fatalf("expected worktree cleanup instructions, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
	`)
	testEqual(t, eff.output.String(), `
		Preparing Go SDK 1.48.0

		[1/6] Fetch main
		      $ git fetch origin main
		[2/6] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
	`)
}

func TestPrepareDraftPRLeavesWorktreeAfterFailure(t *testing.T) {
	changelog := stripIndentation(`
		## [Unreleased]

		### Fixed

		- A fix.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`)
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.47.0"
		)
	`)

	eff := newMockEffects(func(cmd command) (string, error) {
		switch cmd.String() {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case `git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`:
			return "", errors.New("command failed")
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := prepareEverything(eff, "1.48.0", time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC), false)
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected command failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Worktree preserved at "+eff.tempDir) ||
		!strings.Contains(err.Error(), "git branch --delete --force chore/release-1.48.0") {
		t.Fatalf("expected worktree cleanup instructions, got %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(eff.commands.String()),
		"git commit -m \"Prepare release 1.48.0\" -- CHANGELOG.md internal/version.go") {
		t.Fatalf("unexpected final command:\n%s", eff.commands.String())
	}
	if strings.Contains(eff.commands.String(), "git worktree remove") {
		t.Fatal("worktree was removed after failure")
	}
	testEqual(t, eff.output.String(), `
		Preparing Go SDK 1.48.0

		[1/6] Fetch main
		      $ git fetch origin main
		[2/6] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		[3/6] Commit release files
		      $ git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
	`)
}

func TestPrepareDraftPRLeavesDirectoryIfWorktreeCreationFails(t *testing.T) {
	var eff *mockEffects
	eff = newMockEffects(func(cmd command) (string, error) {
		if cmd.String() == "git worktree add -b chore/release-1.48.0 "+eff.tempDir+" origin/main" {
			return "", errors.New("command failed")
		}
		return "", nil
	})

	err := prepareEverything(eff, "1.48.0", time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC), false)
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected worktree creation failure, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
	`)
	testEqual(t, eff.output.String(), `
		Preparing Go SDK 1.48.0

		[1/6] Fetch main
		      $ git fetch origin main
		[2/6] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
	`)
}

func TestPrepareEverything(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	version := "1.48.0"

	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)

	changelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Fixed

		- A fix.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`)
	updatedChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		### Changed

		### Deprecated

		### :boom: Breaking Changes

		### Fixed

		### Security

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`)

	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.47.0"
		)
	`)
	updatedVersionGo := stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)

	var eff *mockEffects
	eff = newMockEffects(func(cmd command) (string, error) {
		switch cmd.String() {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case `git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], updatedChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], updatedVersionGo)
		case `gh pr create --draft --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare Go SDK release 1.48.0."`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case `gh release create v1.48.0 --draft --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Fixed\n\n- A fix.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	// TESTS

	err := prepareEverything(eff, version, date, false)
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
		`+eff.tempDir+`: git push --set-upstream origin chore/release-1.48.0
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare Go SDK release 1.48.0."
		`+eff.tempDir+`: gh release create v1.48.0 --draft --title v1.48.0 --notes-file `+eff.tempDir+`/prepare-release-notes.md --generate-notes
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing Go SDK 1.48.0

		[1/6] Fetch main
		      $ git fetch origin main
		[2/6] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		[3/6] Commit release files
		      $ git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
		[4/6] Push branch and create draft PR
		      $ git push --set-upstream origin chore/release-1.48.0
		      $ gh pr create --draft --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare Go SDK release 1.48.0."
		      PR: https://github.com/temporalio/sdk-go/pull/123
		[5/6] Create draft release
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create v1.48.0 --draft --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		[6/6] Clean up temporary worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.
	`)
}
