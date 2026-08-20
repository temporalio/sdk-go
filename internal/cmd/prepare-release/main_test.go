package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contribEnvconfig is the module used by the contrib tests below.
func contribEnvconfig(t *testing.T) releaseTarget {
	t.Helper()
	target, err := contribTarget("contrib/envconfig")
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestCreateDraftRelease(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/release\n", nil
	})

	args := commandArgs{target: sdkTarget(), version: "1.2.3"}
	releaseURL, err := createDraftRelease(eff, args, "/worktree", "Notes")
	if err != nil {
		t.Fatal(err)
	}
	if releaseURL != "https://example.com/release" {
		t.Fatalf("unexpected release URL: %q", releaseURL)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh release create v1.2.3 --draft --title v1.2.3 --notes Notes --generate-notes
	`)
}

// Contrib releases are tagged with their module prefix and must never take over
// GitHub's "Latest" badge from the main SDK.
func TestCreateDraftReleaseForContribModule(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/release\n", nil
	})

	args := commandArgs{target: contribEnvconfig(t), version: "1.2.3"}
	releaseURL, err := createDraftRelease(eff, args, "/worktree", "Notes")
	if err != nil {
		t.Fatal(err)
	}
	if releaseURL != "https://example.com/release" {
		t.Fatalf("unexpected release URL: %q", releaseURL)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh release create contrib/envconfig/v1.2.3 --draft --title contrib/envconfig/v1.2.3 --notes Notes --latest=false
	`)
}

func TestOpenDraftPR(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/pr\n", nil
	})

	args := commandArgs{target: sdkTarget(), version: "1.2.3"}
	prURL, err := openDraftPR(eff, args, "/worktree", "release")
	if err != nil {
		t.Fatal(err)
	}
	if prURL != "https://example.com/pr" {
		t.Fatalf("unexpected PR URL: %q", prURL)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh pr create --draft --base main --head release --title "Prepare release 1.2.3" --body "Prepare go.temporal.io/sdk release 1.2.3."
	`)
}

func TestOpenDraftPRForContribModule(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "https://example.com/pr\n", nil
	})

	args := commandArgs{target: contribEnvconfig(t), version: "1.2.3"}
	_, err := openDraftPR(eff, args, "/worktree", "release")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: gh pr create --draft --base main --head release --title "Prepare contrib/envconfig release 1.2.3" --body "Prepare go.temporal.io/sdk/contrib/envconfig release 1.2.3."
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

	args := commandArgs{target: sdkTarget(), version: "1.2.3"}
	err := commitRelease(eff, args, "/worktree")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare release 1.2.3" -- CHANGELOG.md internal/version.go
	`)
}

// A contrib release only touches that module's changelog; the SDK version constant
// is unrelated to it.
func TestCommitReleaseForContribModule(t *testing.T) {
	eff := newMockEffects(nil)

	args := commandArgs{target: contribEnvconfig(t), version: "1.2.3"}
	err := commitRelease(eff, args, "/worktree")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare contrib/envconfig release 1.2.3" -- contrib/envconfig/CHANGELOG.md
	`)
}

func TestCreateDraftPRStopsBeforePush(t *testing.T) {
	eff := newMockEffects(nil)

	args := commandArgs{target: sdkTarget(), version: "1.2.3", stopBeforePush: true}
	_, err := createDraftPR(eff, args, "/worktree", "release")
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
		Created worktree: /tmp/prepare-go-release-123456 at HEAD: Initial commit (abc123)
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

// Release tags are the only record of a contrib module's current version, so the
// fetch has to bring them along.
func TestFetchMain(t *testing.T) {
	eff := newMockEffects(nil)

	err := fetchMain(eff, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `/repo: git fetch --tags origin main`)
}

func TestListTags(t *testing.T) {
	eff := newMockEffects(func(command) (string, error) {
		return "contrib/envconfig/v1.0.0\n\ncontrib/envconfig/v1.0.1\n", nil
	})

	tags, err := listTags(eff, "/repo", "contrib/envconfig/v*")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tags, ",") != "contrib/envconfig/v1.0.0,contrib/envconfig/v1.0.1" {
		t.Fatalf("unexpected tags: %q", tags)
	}
	testEqual(t, eff.commands.String(), `/repo: git tag --list contrib/envconfig/v*`)
}

func TestValidateReleaseRejectsNonIncreasingVersion(t *testing.T) {
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

	args := commandArgs{target: sdkTarget(), version: "1.47.0"}
	err := validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "does not follow the current version 1.47.0") {
		t.Fatalf("expected version increase error, got %v", err)
	}
	testEqual(t, eff.output.String(), "")
}

func TestValidateReleaseRejectsInvalidChangelog(t *testing.T) {
	eff := newMockEffects(nil)
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\nrequire go.temporal.io/api v1.63.4\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.47.0"`
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = "## [Unreleased]\n\n## [1.47.0]\n\n## [1.46.0]\n\n## [1.46.0]\n"

	args := commandArgs{target: sdkTarget(), version: "1.48.0"}
	err := validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), `found 2 sections for "1.46.0"`) {
		t.Fatalf("expected duplicate changelog section error, got %v", err)
	}
}

// A misspelled module directory has no go.mod, which is what stops the tool from
// tagging and releasing a module that does not exist.
func TestValidateReleaseRejectsUnknownContribModule(t *testing.T) {
	eff := newMockEffects(nil)
	target, err := contribTarget("contrib/envconfg")
	if err != nil {
		t.Fatal(err)
	}

	args := commandArgs{target: target, version: "1.0.3"}
	err = validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "contrib/envconfg is not a Go module in this repository") {
		t.Fatalf("expected unknown module error, got %v", err)
	}
	if strings.Contains(eff.commands.String(), "git tag") {
		t.Fatalf("unknown module was inspected further:\n%s", eff.commands.String())
	}
}

// A directory holding some other module would otherwise be released under the wrong tag.
func TestValidateReleaseRejectsMismatchedModulePath(t *testing.T) {
	eff := newMockEffects(nil)
	target := contribEnvconfig(t)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] =
		"module go.temporal.io/sdk/contrib/tally\nrequire go.temporal.io/sdk v1.48.0\n"

	args := commandArgs{target: target, version: "1.0.3"}
	err := validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), `declares module "go.temporal.io/sdk/contrib/tally", expected "go.temporal.io/sdk/contrib/envconfig"`) {
		t.Fatalf("expected module path mismatch error, got %v", err)
	}
}

// A contrib module cannot be released against an unreleased SDK.
func TestValidateReleaseRejectsUnreleasedSDKDependency(t *testing.T) {
	eff := newMockEffects(nil)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.1-0.20260804123456-abcdef123456
	`)

	args := commandArgs{target: contribEnvconfig(t), version: "1.0.3"}
	err := validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "go.temporal.io/sdk must use an official release") {
		t.Fatalf("expected dependency validation error, got %v", err)
	}
}

func TestValidateReleaseRejectsAlreadyReleasedVersion(t *testing.T) {
	eff := newMockEffects(func(cmd command) (string, error) {
		if cmd.String() == "git tag --list contrib/envconfig/v*" {
			return "contrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.3\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)

	args := commandArgs{target: contribEnvconfig(t), version: "1.0.3"}
	err := validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "tag contrib/envconfig/v1.0.3 exists") {
		t.Fatalf("expected already-released error, got %v", err)
	}
}

// Contrib modules carry no version constant, so their current version comes from tags.
func TestValidateReleaseUsesTagsForContribVersion(t *testing.T) {
	eff := newMockEffects(func(cmd command) (string, error) {
		if cmd.String() == "git tag --list contrib/envconfig/v*" {
			// Out of order, and including a nested module's tags.
			return "contrib/envconfig/v1.0.2\ncontrib/envconfig/v0.1.0\ncontrib/envconfig/v1.0.0\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] =
		"## [Unreleased]\n\n## [1.0.2] - 2026-07-28\n"

	args := commandArgs{target: contribEnvconfig(t), version: "1.0.3"}
	err := validateRelease(eff, args, eff.tempDir)
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		Preparing contrib/envconfig 1.0.3, following 1.0.2 (tag contrib/envconfig/v1.0.3).
	`)

	args.version = "1.2.0"
	err = validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "does not follow the current version 1.0.2") {
		t.Fatalf("expected version increase error, got %v", err)
	}
}

// An untagged contrib module has no previous release section to validate against.
func TestValidateReleaseAllowsFirstContribRelease(t *testing.T) {
	eff := newMockEffects(nil)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = "## [Unreleased]\n"

	args := commandArgs{target: contribEnvconfig(t), version: "0.1.0"}
	err := validateRelease(eff, args, eff.tempDir)
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		Preparing the first release of contrib/envconfig: 0.1.0 (tag contrib/envconfig/v0.1.0).
	`)

	args.version = "0.4.0"
	err = validateRelease(eff, args, eff.tempDir)
	if err == nil || !strings.Contains(err.Error(), "cannot be a first release") {
		t.Fatalf("expected first release error, got %v", err)
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

	args := commandArgs{target: sdkTarget(), version: "1.48.0", releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}
	err := prepareEverything(eff, args)
	if err == nil || !strings.Contains(err.Error(), "must use an official release") {
		t.Fatalf("expected API version validation failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Worktree preserved at "+eff.tempDir) ||
		!strings.Contains(err.Error(), "git branch --delete --force chore/release-1.48.0") {
		t.Fatalf("expected worktree cleanup instructions, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
	`)
	testEqual(t, eff.output.String(), `
		Created worktree: /tmp/prepare-go-release-123456 at HEAD: Initial commit (abc123)
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

	args := commandArgs{target: sdkTarget(), version: "1.48.0", releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}
	err := prepareEverything(eff, args)
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
		Created worktree: /tmp/prepare-go-release-123456 at HEAD: Initial commit (abc123)
		Preparing the Go SDK module 1.48.0, following 1.47.0 (tag v1.48.0).
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

	args := commandArgs{target: sdkTarget(), version: "1.48.0", releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}
	err := prepareEverything(eff, args)
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected worktree creation failure, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
	`)
	testEqual(t, eff.output.String(), "")
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
		case "git tag --list v*":
			return "v1.46.0\nv1.47.0\n", nil
		case `git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], updatedChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], updatedVersionGo)
		case `gh pr create --draft --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare go.temporal.io/sdk release 1.48.0."`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case `gh release create v1.48.0 --draft --title v1.48.0 --notes "# Highlights\n\n### Fixed\n\n- A fix.\n" --generate-notes`:
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	// TESTS

	args := commandArgs{target: sdkTarget(), version: version, releaseDate: date}
	err := prepareEverything(eff, args)
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
		`+eff.tempDir+`: git push --set-upstream origin chore/release-1.48.0
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare go.temporal.io/sdk release 1.48.0."
		`+eff.tempDir+`: gh release create v1.48.0 --draft --title v1.48.0 --notes "# Highlights\n\n### Fixed\n\n- A fix.\n" --generate-notes
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Created worktree: /tmp/prepare-go-release-123456 at HEAD: Initial commit (abc123)
		Preparing the Go SDK module 1.48.0, following 1.47.0 (tag v1.48.0).
		PR: https://github.com/temporalio/sdk-go/pull/123
		Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		Cleaned up worktree.
	`)
}

func TestPrepareEverythingForContribModule(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	target := contribEnvconfig(t)

	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
		replace go.temporal.io/sdk => ../../
	`)

	changelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		- A feature.

		### Fixed

		## [1.0.2] - 2026-07-28

		### Fixed

		- An older fix.
	`)
	// Contrib changelogs are not reseeded with the SDK's section headers, and empty
	// sections are dropped from the release.
	updatedChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		## [1.0.3] - 2026-08-04

		### Added

		- A feature.

		## [1.0.2] - 2026-07-28

		### Fixed

		- An older fix.
	`)

	changelogPath := filepath.Join(mockTempDir, "contrib", "envconfig", "CHANGELOG.md")
	goModPath := filepath.Join(mockTempDir, "contrib", "envconfig", "go.mod")

	var eff *mockEffects
	eff = newMockEffects(func(cmd command) (string, error) {
		switch cmd.String() {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.0\ncontrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.1\n", nil
		case `git commit -m "Prepare contrib/envconfig release 1.0.3" -- contrib/envconfig/CHANGELOG.md`:
			testEqual(t, eff.files[changelogPath], updatedChangelog)
			testEqual(t, eff.files[goModPath], goMod)
		case `gh pr create --draft --base main --head chore/release-contrib-envconfig-1.0.3 --title "Prepare contrib/envconfig release 1.0.3" --body "Prepare go.temporal.io/sdk/contrib/envconfig release 1.0.3."`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case `gh release create contrib/envconfig/v1.0.3 --draft --title contrib/envconfig/v1.0.3 --notes "# Highlights\n\n### Added\n\n- A feature.\n" --latest=false`:
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	})

	eff.files[changelogPath] = changelog
	eff.files[goModPath] = goMod

	args := commandArgs{target: target, version: "1.0.3", releaseDate: date}
	err := prepareEverything(eff, args)
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-contrib-envconfig-1.0.3 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: git commit -m "Prepare contrib/envconfig release 1.0.3" -- contrib/envconfig/CHANGELOG.md
		`+eff.tempDir+`: git push --set-upstream origin chore/release-contrib-envconfig-1.0.3
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-contrib-envconfig-1.0.3 --title "Prepare contrib/envconfig release 1.0.3" --body "Prepare go.temporal.io/sdk/contrib/envconfig release 1.0.3."
		`+eff.tempDir+`: gh release create contrib/envconfig/v1.0.3 --draft --title contrib/envconfig/v1.0.3 --notes "# Highlights\n\n### Added\n\n- A feature.\n" --latest=false
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Created worktree: /tmp/prepare-go-release-123456 at HEAD: Initial commit (abc123)
		Preparing contrib/envconfig 1.0.3, following 1.0.2 (tag contrib/envconfig/v1.0.3).
		PR: https://github.com/temporalio/sdk-go/pull/123
		Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		Cleaned up worktree.
	`)
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantDir string
		wantErr string
	}{
		{name: "SDK version only", args: []string{"1.48.0"}},
		{name: "contrib module and version", args: []string{"contrib/envconfig", "1.0.3"}, wantDir: "contrib/envconfig"},
		{name: "nested contrib module", args: []string{"contrib/aws/s3driver/awssdkv2", "0.2.2"}, wantDir: "contrib/aws/s3driver/awssdkv2"},
		{name: "trailing slash", args: []string{"contrib/envconfig/", "1.0.3"}, wantDir: "contrib/envconfig"},
		{name: "no arguments", args: nil, wantErr: "usage:"},
		{name: "too many arguments", args: []string{"contrib/envconfig", "1.0.3", "extra"}, wantErr: "usage:"},
		{name: "non-contrib module", args: []string{"internal", "1.0.3"}, wantErr: "invalid module"},
		{name: "escaping module", args: []string{"contrib/../internal", "1.0.3"}, wantErr: "invalid module"},
		{name: "module without version", args: []string{"contrib/envconfig"}, wantErr: "invalid version"},
		{name: "swapped arguments", args: []string{"1.0.3", "contrib/envconfig"}, wantErr: "invalid module"},
		{name: "invalid date", args: []string{"--date", "August 4", "1.48.0"}, wantErr: "invalid release date"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseArgs(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.target.dir != test.wantDir {
				t.Fatalf("unexpected module directory: got %q, want %q", got.target.dir, test.wantDir)
			}
		})
	}
}
