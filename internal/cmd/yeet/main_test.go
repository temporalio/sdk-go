package main

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewApp(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantDirs []string
		wantErr  string
	}{
		{name: "lint checkout", args: []string{"lint"}, wantDirs: []string{}},
		{name: "lint commit", args: []string{"lint", "abc123"}, wantDirs: []string{}},
		{name: "lint extra arguments", args: []string{"lint", "abc123", "extra"}, wantErr: "usage:"},
		{name: "lint rejects PR flags", args: []string{"lint", "--release-date", "2026-08-04"}, wantErr: "flag provided but not defined"},
		{name: "SDK PR", args: []string{"pr", ".=1.48.0"}},
		{name: "contrib PR", args: []string{"pr", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "nested contrib module", args: []string{"pr", "contrib/aws/s3driver/awssdkv2=0.2.2"}, wantDirs: []string{"contrib/aws/s3driver/awssdkv2"}},
		{name: "trailing slash", args: []string{"pr", "contrib/envconfig/=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "SDK release", args: []string{"release", "abc123", ".=1.48.0"}},
		{name: "contrib release", args: []string{"release", "abc123", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "no arguments", args: nil, wantErr: "usage:"},
		{name: "unknown command", args: []string{"unknown"}, wantErr: "unknown command"},
		{name: "old invocation", args: []string{"1.48.0"}, wantErr: "unknown command"},
		{name: "missing assignment", args: []string{"pr"}, wantErr: "usage:"},
		{name: "missing commit", args: []string{"release", ".=1.48.0"}, wantErr: "usage:"},
		{name: "multiple PR assignments", args: []string{"pr", ".=1.48.0", "contrib/envconfig=1.0.3"}, wantDirs: []string{"", "contrib/envconfig"}},
		{name: "multiple release assignments", args: []string{"release", "abc123", ".=1.48.0", "contrib/envconfig=1.0.3"}, wantDirs: []string{"", "contrib/envconfig"}},
		{name: "duplicate SDK", args: []string{"pr", ".=1.48.0", ".=1.48.1"}, wantErr: "specified more than once"},
		{name: "duplicate contrib", args: []string{"pr", "contrib/envconfig=1.0.3", "./contrib/envconfig/=1.1.0"}, wantErr: "specified more than once"},
		{name: "duplicate release module", args: []string{"release", "abc123", ".=1.48.0", ".=1.48.0"}, wantErr: "specified more than once"},
		{name: "invalid second version", args: []string{"pr", ".=1.48.0", "contrib/envconfig=bad"}, wantErr: "invalid version"},
		{name: "non-contrib module", args: []string{"pr", "internal=1.0.3"}, wantErr: "invalid module"},
		{name: "escaping module", args: []string{"pr", "contrib/../internal=1.0.3"}, wantErr: "invalid module"},
		{name: "missing equals", args: []string{"pr", "contrib/envconfig"}, wantErr: "expected MODULE=VERSION"},
		{name: "empty module", args: []string{"pr", "=1.0.3"}, wantErr: "invalid module"},
		{name: "empty version", args: []string{"pr", "contrib/envconfig="}, wantErr: "invalid version"},
		{name: "extra equals", args: []string{"pr", ".=1.48.0=1.49.0"}, wantErr: "invalid version"},
		{name: "invalid date", args: []string{"pr", "--release-date", "August 4", ".=1.48.0"}, wantErr: "invalid release date"},
		{name: "release rejects PR flags", args: []string{"release", "--release-date", "2026-08-04", "abc123", ".=1.48.0"}, wantErr: "flag provided but not defined"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newApp(io.Discard, io.Discard, test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.command != test.args[0] {
				t.Fatalf("unexpected command: got %q, want %q", got.command, test.args[0])
			}
			if (got.command == "release" || (got.command == "lint" && len(test.args) == 2)) && got.commit != "abc123" {
				t.Fatalf("unexpected commit: %q", got.commit)
			}
			if got.command == "lint" && len(test.args) == 1 && got.commit != "" {
				t.Fatalf("unexpected commit for checkout lint: %q", got.commit)
			}
			wantDirs := test.wantDirs
			if wantDirs == nil {
				wantDirs = []string{""}
			}
			if len(got.releases) != len(wantDirs) {
				t.Fatalf("unexpected release count: got %d, want %d", len(got.releases), len(wantDirs))
			}
			for i, release := range got.releases {
				if gotDir := release.target.dir; gotDir != wantDirs[i] {
					t.Fatalf("unexpected module directory: got %q, want %q", gotDir, wantDirs[i])
				}
			}

		})
	}
}

func TestPreparePR(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	version := semver{1, 48, 0}

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

	a, eff := newMockApp(app{releases: []releaseSpec{{target: sdkTarget(), version: version}}, releaseDate: date}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list v*":
			return "v1.46.0\nv1.47.0\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\n.=1.48.0" -- CHANGELOG.md internal/version.go`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], updatedChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], updatedVersionGo)
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body .=1.48.0`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		}
		return "", nil
	}

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	// TESTS

	err := a.preparePR()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git commit -m "Prepare release 2026-08-04\n\n.=1.48.0" -- CHANGELOG.md internal/version.go
		`+eff.tempDir+`: git push --set-upstream origin chore/release-2026-08-04
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body .=1.48.0
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing .=1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 2026-08-04\n\n.=1.48.0" -- CHANGELOG.md internal/version.go
		[4/5] Publish draft PR
		      $ git push --set-upstream origin chore/release-2026-08-04
		      $ gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body .=1.48.0
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123
		[5/5] Clean up release worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.

		After merging the PR, create draft releases with:
		  yeet release <MERGE_COMMIT> .=1.48.0

		To close the PR and delete the branch:
		  gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
	`)
}

func TestPreparePRForContribModule(t *testing.T) {
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
	updatedChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		### Changed

		### Deprecated

		### :boom: Breaking Changes

		### Fixed

		### Security

		## [1.0.3] - 2026-08-04

		### Added

		- A feature.

		## [1.0.2] - 2026-07-28

		### Fixed

		- An older fix.
	`)

	changelogPath := filepath.Join(mockTempDir, "contrib", "envconfig", "CHANGELOG.md")
	goModPath := filepath.Join(mockTempDir, "contrib", "envconfig", "go.mod")

	a, eff := newMockApp(app{releases: []releaseSpec{{target: target, version: semver{1, 0, 3}}}, releaseDate: date}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.0\ncontrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.1\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\ncontrib/envconfig=1.0.3" -- contrib/envconfig/CHANGELOG.md`:
			testEqual(t, eff.files[changelogPath], updatedChangelog)
			testEqual(t, eff.files[goModPath], goMod)
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body contrib/envconfig=1.0.3`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		}
		return "", nil
	}

	eff.files[changelogPath] = changelog
	eff.files[goModPath] = goMod

	err := a.preparePR()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: git commit -m "Prepare release 2026-08-04\n\ncontrib/envconfig=1.0.3" -- contrib/envconfig/CHANGELOG.md
		`+eff.tempDir+`: git push --set-upstream origin chore/release-2026-08-04
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body contrib/envconfig=1.0.3
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing contrib/envconfig=1.0.3

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list contrib/envconfig/v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 2026-08-04\n\ncontrib/envconfig=1.0.3" -- contrib/envconfig/CHANGELOG.md
		[4/5] Publish draft PR
		      $ git push --set-upstream origin chore/release-2026-08-04
		      $ gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body contrib/envconfig=1.0.3
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123
		[5/5] Clean up release worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.

		After merging the PR, create draft releases with:
		  yeet release <MERGE_COMMIT> contrib/envconfig=1.0.3

		To close the PR and delete the branch:
		  gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
	`)
}

func TestPreparePRForMultipleModules(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	releases := []releaseSpec{
		{target: sdkTarget(), version: semver{1, 48, 0}},
		{target: contribEnvconfig(t), version: semver{1, 0, 3}},
	}

	changelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Fixed

		- A fix.
	`)
	updatedSDKChangelog := stripIndentation(`
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
	`)
	updatedContribChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		### Changed

		### Deprecated

		### :boom: Breaking Changes

		### Fixed

		### Security

		## [1.0.3] - 2026-08-04

		### Fixed

		- A fix.
	`)

	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	contribGoMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.47.0
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

	a, eff := newMockApp(app{releases: releases, releaseDate: date}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\n.=1.48.0 contrib/envconfig=1.0.3" -- CHANGELOG.md internal/version.go contrib/envconfig/CHANGELOG.md`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], updatedSDKChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")], updatedContribChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")], contribGoMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], updatedVersionGo)
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body ".=1.48.0 contrib/envconfig=1.0.3"`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		}
		return "", nil
	}

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = contribGoMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := a.preparePR()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: git commit -m "Prepare release 2026-08-04\n\n.=1.48.0 contrib/envconfig=1.0.3" -- CHANGELOG.md internal/version.go contrib/envconfig/CHANGELOG.md
		`+eff.tempDir+`: git push --set-upstream origin chore/release-2026-08-04
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body ".=1.48.0 contrib/envconfig=1.0.3"
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing .=1.48.0 contrib/envconfig=1.0.3

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list v*
		      $ git tag --list contrib/envconfig/v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 2026-08-04\n\n.=1.48.0 contrib/envconfig=1.0.3" -- CHANGELOG.md internal/version.go contrib/envconfig/CHANGELOG.md
		[4/5] Publish draft PR
		      $ git push --set-upstream origin chore/release-2026-08-04
		      $ gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body ".=1.48.0 contrib/envconfig=1.0.3"
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123
		[5/5] Clean up release worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.

		After merging the PR, create draft releases with:
		  yeet release <MERGE_COMMIT> .=1.48.0 contrib/envconfig=1.0.3

		To close the PR and delete the branch:
		  gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
	`)
}

func TestPreparePRAllowsDependencyFixes(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	release := releaseSpec{target: contribEnvconfig(t), version: semver{0, 2, 0}}
	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0-0.20260804123456-abcdef123456
	`)
	changelog := stripIndentation(`
		## [Unreleased]

		### Fixed

		- A fix.
	`)
	updatedChangelog := stripIndentation(`
		## [Unreleased]

		### Added

		### Changed

		### Deprecated

		### :boom: Breaking Changes

		### Fixed

		### Security

		## [0.2.0] - 2026-08-04

		### Fixed

		- A fix.
	`)
	a, eff := newMockApp(app{releases: []releaseSpec{release}, releaseDate: date}, func(cmd string) (string, error) {
		switch cmd {
		case "git ls-files -z -- go.mod **/go.mod":
			return "contrib/envconfig/go.mod\x00", nil
		case "git tag --list *", "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v0.1.0\n", nil
		case "git diff --name-only --no-renames -z contrib/envconfig/v0.1.0 -- contrib/envconfig":
			return "contrib/envconfig/go.mod\x00", nil
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body contrib/envconfig=0.2.0`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case "gh release create contrib/envconfig/v0.2.0 --draft --target abc123 --title contrib/envconfig/v0.2.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	})
	published := false
	eff.moduleLookupHandler = func(string, string) (bool, error) { return published, nil }
	goModPath := filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")
	changelogPath := filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")
	eff.files[goModPath] = goMod
	eff.files[changelogPath] = changelog
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "go.mod")] = goMod
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "CHANGELOG.md")] = changelog

	err := a.lint()
	if err == nil || !strings.Contains(err.Error(), "must use an official release") {
		t.Fatalf("expected dependency readiness failure, got %v", err)
	}
	err = a.preparePR()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.files[goModPath], goMod)
	testEqual(t, eff.files[changelogPath], updatedChangelog)
	if !strings.Contains(eff.output.String(), "Draft PR: https://github.com/temporalio/sdk-go/pull/123") {
		t.Fatalf("missing draft PR:\n%s", eff.output.String())
	}

	// Fix the dependency in the PR, then inspect its commit.
	eff.files[goModPath] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	a.commit = "abc123"
	err = a.lint()
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("expected unpublished dependency failure, got %v", err)
	}
	err = a.prepareRelease()
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("expected release to reject unpublished dependency, got %v", err)
	}
	if strings.Contains(eff.commands.String(), "gh release create") {
		t.Fatalf("release was created before dependencies were ready:\n%s", eff.commands.String())
	}

	published = true
	err = a.lint()
	if err != nil {
		t.Fatal(err)
	}
	err = a.prepareRelease()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Fixed\n\n- A fix.\n")
	if !strings.Contains(eff.output.String(), "Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc") {
		t.Fatalf("missing draft release:\n%s", eff.output.String())
	}
}

func TestPreparePRValidatesAllModulesBeforeWriting(t *testing.T) {
	releases := []releaseSpec{
		{target: sdkTarget(), version: semver{1, 48, 0}},
		{target: contribEnvconfig(t), version: semver{1, 0, 3}},
	}
	changelog := stripIndentation(`
		## [Unreleased]

		### Fixed

		- A fix.
	`)
	a, eff := newMockApp(app{releases: releases, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		switch cmd {
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\n"
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = "module go.temporal.io/sdk/contrib/envconfig\n"
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = "## [Unreleased]\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.47.0"`

	err := a.preparePR()
	if err == nil || !strings.Contains(err.Error(), "appears to be empty") {
		t.Fatalf("expected empty contrib changelog error, got %v", err)
	}
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], changelog)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")], "## [Unreleased]\n")
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], `SDKVersion = "1.47.0"`)
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
	`)
}

func TestPreparePRLeavesDirectoryIfWorktreeCreationFails(t *testing.T) {
	a, eff := newMockApp(app{releases: []releaseSpec{{target: sdkTarget(), version: semver{1, 48, 0}}}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		if cmd == "git worktree add -b chore/release-2026-08-04 "+eff.tempDir+" origin/main" {
			return "", errors.New("command failed")
		}
		return "", nil
	}

	err := a.preparePR()
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected worktree creation failure, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
	`)
	testEqual(t, eff.output.String(), `
		Preparing .=1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
	`)
}

func TestPreparePRValidatesBeforePushingBranch(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
	`)

	a, eff := newMockApp(app{releases: []releaseSpec{{target: sdkTarget(), version: semver{1, 48, 0}}}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		if cmd == `git log -1 "--format=%s (%h)"` {
			return "Initial commit (abc123)\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod

	err := a.preparePR()
	if err == nil || !strings.Contains(err.Error(), `expected "go.temporal.io/sdk"`) {
		t.Fatalf("expected module path validation failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Worktree preserved at "+eff.tempDir) ||
		!strings.Contains(err.Error(), "git branch --delete --force chore/release-2026-08-04") {
		t.Fatalf("expected worktree cleanup instructions, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-2026-08-04 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
	`)
	testEqual(t, eff.output.String(), `
		Preparing .=1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
	`)
}

func TestPreparePRLeavesWorktreeAfterFailure(t *testing.T) {
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

	a, eff := newMockApp(app{releases: []releaseSpec{{target: sdkTarget(), version: semver{1, 48, 0}}}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\n.=1.48.0" -- CHANGELOG.md internal/version.go`:
			return "", errors.New("command failed")
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := a.preparePR()
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected command failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "Worktree preserved at "+eff.tempDir) ||
		!strings.Contains(err.Error(), "git branch --delete --force chore/release-2026-08-04") {
		t.Fatalf("expected worktree cleanup instructions, got %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(eff.commands.String()),
		"git commit -m \"Prepare release 2026-08-04\\n\\n.=1.48.0\" -- CHANGELOG.md internal/version.go") {
		t.Fatalf("unexpected final command:\n%s", eff.commands.String())
	}
	if strings.Contains(eff.commands.String(), "git worktree remove") {
		t.Fatal("worktree was removed after failure")
	}
	testEqual(t, eff.output.String(), `
		Preparing .=1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 2026-08-04\n\n.=1.48.0" -- CHANGELOG.md internal/version.go
	`)
}

func TestPrepareRelease(t *testing.T) {
	version := semver{1, 48, 0}
	commit := "1234567890abcdef1234567890abcdef12345678"

	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)

	changelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		### Fixed

		## [1.48.0] - 2026-08-04

		### Fixed

		- Notes edited in the PR.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`)

	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)

	a, eff := newMockApp(app{releases: []releaseSpec{{target: sdkTarget(), version: version}}, commit: "1234567"}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case `git log -1 "--format=%s (%h)"`:
			return "Prepare release 1.48.0 (1234567)\n", nil
		case "git tag --list v*":
			return "v1.46.0\nv1.47.0\n", nil
		case "gh release create v1.48.0 --draft --target " + commit + " --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes":
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Fixed\n\n- Notes edited in the PR.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	}

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo
	eff.files[filepath.Join(mockRepoRoot, "CHANGELOG.md")] = "Local edits must not become release notes."

	err := a.prepareRelease()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], changelog)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], versionGo)
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git rev-parse --verify --end-of-options 1234567^{commit}
		/repo: git merge-base --is-ancestor `+commit+` origin/main
		/repo: git worktree add --detach `+eff.tempDir+` `+commit+`
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: gh release create v1.48.0 --draft --target `+commit+` --title v1.48.0 --notes-file `+eff.tempDir+`/prepare-release-notes.md --generate-notes
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Creating draft releases for .=1.48.0

		[1/4] Fetch main
		      $ git fetch --tags origin main
		[2/4] Check out release commit
		      $ git rev-parse --verify --end-of-options 1234567^{commit}
		      $ git merge-base --is-ancestor `+commit+` origin/main
		      $ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Prepare release 1.48.0 (1234567)
		[3/4] Validate release files
		      $ git tag --list v*
		[4/4] Create draft releases
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create v1.48.0 --draft --target `+commit+` --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		      $ git worktree remove --force /tmp/prepare-go-release-123456

		To roll back these releases, run:
		  gh release delete v1.48.0 --yes
	`)
}

func TestPrepareReleaseForContribModule(t *testing.T) {
	target := contribEnvconfig(t)
	commit := "1234567890abcdef1234567890abcdef12345678"

	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
		replace go.temporal.io/sdk => ../../
	`)

	changelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		### Added

		### Fixed

		## [1.0.3] - 2026-08-04

		### Added

		- A feature.

		## [1.0.2] - 2026-07-28

		### Fixed

		- An older fix.
	`)

	changelogPath := filepath.Join(mockTempDir, "contrib", "envconfig", "CHANGELOG.md")
	goModPath := filepath.Join(mockTempDir, "contrib", "envconfig", "go.mod")

	a, eff := newMockApp(app{releases: []releaseSpec{{target: target, version: semver{1, 0, 3}}}, commit: "1234567"}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case `git log -1 "--format=%s (%h)"`:
			return "Prepare contrib/envconfig release 1.0.3 (1234567)\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.0\ncontrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.1\n", nil
		case "gh release create contrib/envconfig/v1.0.3 --draft --target " + commit + " --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Added\n\n- A feature.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	}

	eff.files[changelogPath] = changelog
	eff.files[goModPath] = goMod

	err := a.prepareRelease()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.files[changelogPath], changelog)
	testEqual(t, eff.files[goModPath], goMod)
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git rev-parse --verify --end-of-options 1234567^{commit}
		/repo: git merge-base --is-ancestor `+commit+` origin/main
		/repo: git worktree add --detach `+eff.tempDir+` `+commit+`
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: gh release create contrib/envconfig/v1.0.3 --draft --target `+commit+` --title contrib/envconfig/v1.0.3 --notes-file `+eff.tempDir+`/prepare-release-notes.md --latest=false
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Creating draft releases for contrib/envconfig=1.0.3

		[1/4] Fetch main
		      $ git fetch --tags origin main
		[2/4] Check out release commit
		      $ git rev-parse --verify --end-of-options 1234567^{commit}
		      $ git merge-base --is-ancestor `+commit+` origin/main
		      $ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Prepare contrib/envconfig release 1.0.3 (1234567)
		[3/4] Validate release files
		      $ git tag --list contrib/envconfig/v*
		[4/4] Create draft releases
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create contrib/envconfig/v1.0.3 --draft --target `+commit+` --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		      $ git worktree remove --force /tmp/prepare-go-release-123456

		To roll back these releases, run:
		  gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestPrepareReleaseForMultipleModules(t *testing.T) {
	commit := "1234567890abcdef1234567890abcdef12345678"
	releases := []releaseSpec{
		{target: sdkTarget(), version: semver{1, 48, 0}},
		{target: contribEnvconfig(t), version: semver{1, 0, 3}},
	}

	sdkChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		## [1.48.0] - 2026-08-04

		### Fixed

		- An SDK fix.
	`)
	contribChangelog := stripIndentation(`
		# Changelog

		## [Unreleased]

		## [1.0.3] - 2026-08-04

		### Added

		- A contrib feature.
	`)
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	contribGoMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.47.0
	`)
	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)

	a, eff := newMockApp(app{releases: releases, commit: "1234567"}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case `git log -1 "--format=%s (%h)"`:
			return "Prepare releases (1234567)\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		case "gh release create v1.48.0 --draft --target " + commit + " --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes":
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Fixed\n\n- An SDK fix.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk\n", nil
		case "gh release create contrib/envconfig/v1.0.3 --draft --target " + commit + " --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Added\n\n- A contrib feature.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib\n", nil
		}
		return "", nil
	}

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = sdkChangelog
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = contribChangelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = contribGoMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := a.prepareRelease()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], sdkChangelog)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")], contribChangelog)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")], contribGoMod)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], versionGo)
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git rev-parse --verify --end-of-options 1234567^{commit}
		/repo: git merge-base --is-ancestor `+commit+` origin/main
		/repo: git worktree add --detach `+eff.tempDir+` `+commit+`
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: gh release create v1.48.0 --draft --target `+commit+` --title v1.48.0 --notes-file `+eff.tempDir+`/prepare-release-notes.md --generate-notes
		`+eff.tempDir+`: gh release create contrib/envconfig/v1.0.3 --draft --target `+commit+` --title contrib/envconfig/v1.0.3 --notes-file `+eff.tempDir+`/prepare-release-notes.md --latest=false
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Creating draft releases for .=1.48.0 contrib/envconfig=1.0.3

		[1/4] Fetch main
		      $ git fetch --tags origin main
		[2/4] Check out release commit
		      $ git rev-parse --verify --end-of-options 1234567^{commit}
		      $ git merge-base --is-ancestor `+commit+` origin/main
		      $ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Prepare releases (1234567)
		[3/4] Validate release files
		      $ git tag --list v*
		      $ git tag --list contrib/envconfig/v*
		[4/4] Create draft releases
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create v1.48.0 --draft --target `+commit+` --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create contrib/envconfig/v1.0.3 --draft --target `+commit+` --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib
		      $ git worktree remove --force /tmp/prepare-go-release-123456

		To roll back these releases, run:
		  gh release delete v1.48.0 --yes
		  gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestPrepareReleaseValidatesAllModulesBeforePublishing(t *testing.T) {
	releases := []releaseSpec{
		{target: sdkTarget(), version: semver{1, 48, 0}},
		{target: contribEnvconfig(t), version: semver{1, 0, 3}},
	}
	changelog := stripIndentation(`
		## [Unreleased]

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix.
	`)
	a, eff := newMockApp(app{releases: releases, commit: "abc123"}, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\n"
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = "module go.temporal.io/sdk/contrib/envconfig\n"
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = "## [Unreleased]\n\n## [1.0.3] - 2026-08-04\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.48.0"`

	err := a.prepareRelease()
	if err == nil || !strings.Contains(err.Error(), "appears to be empty") {
		t.Fatalf("expected empty contrib release notes error, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git rev-parse --verify --end-of-options abc123^{commit}
		/repo: git merge-base --is-ancestor abc123 origin/main
		/repo: git worktree add --detach `+eff.tempDir+` abc123
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		/repo: git worktree remove --force `+eff.tempDir,
	)
}

func TestPrepareReleaseReportsCreatedDraftsOnFailure(t *testing.T) {
	tally, err := contribTarget("contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	releases := []releaseSpec{
		{target: contribEnvconfig(t), version: semver{0, 1, 0}},
		{target: tally, version: semver{0, 1, 0}},
	}
	changelog := stripIndentation(`
		## [Unreleased]

		## [0.1.0] - 2026-08-04

		### Added

		- A feature.
	`)
	a, eff := newMockApp(app{releases: releases, commit: "abc123"}, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case "gh release create contrib/envconfig/v0.1.0 --draft --target abc123 --title contrib/envconfig/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-envconfig\n", nil
		case "gh release create contrib/tally/v0.1.0 --draft --target abc123 --title contrib/tally/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "", errors.New("command failed")
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = "module go.temporal.io/sdk/contrib/envconfig\n"
	eff.files[filepath.Join(eff.tempDir, "contrib", "tally", "go.mod")] = "module go.temporal.io/sdk/contrib/tally\n"
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "contrib", "tally", "CHANGELOG.md")] = changelog

	err = a.prepareRelease()
	if err == nil || !strings.Contains(err.Error(), "create draft release contrib/tally/v0.1.0") {
		t.Fatalf("expected tally draft release failure, got %v", err)
	}
	before, rollback, ok := strings.Cut(eff.output.String(), "To roll back these releases, run:\n")
	if !ok {
		t.Fatalf("missing rollback commands:\n%s", eff.output.String())
	}
	if !strings.HasSuffix(strings.TrimSpace(before), "git worktree remove --force "+eff.tempDir) {
		t.Fatalf("rollback commands were not preceded by worktree cleanup:\n%s", eff.output.String())
	}
	testEqual(t, rollback, `
		  gh release delete contrib/envconfig/v0.1.0 --yes
	`)
}

func TestLintCheckout(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	changelog := stripIndentation(`
		## [Unreleased]

		### Fixed

		- A fix.
	`)
	a, eff := newMockApp(app{releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		switch cmd {
		case "git ls-files -z -- go.mod **/go.mod":
			return "contrib/envconfig/go.mod\x00", nil
		case "git tag --list *", "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		case "git diff --name-only --no-renames -z contrib/envconfig/v1.0.2 -- contrib/envconfig":
			return "contrib/envconfig/file.go\x00", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "go.mod")] = goMod
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "CHANGELOG.md")] = changelog

	err := a.lint()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "go.mod")], goMod)
	testEqual(t, eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "CHANGELOG.md")], changelog)
	if len(eff.files) != 2 {
		t.Fatalf("lint wrote files: %v", eff.files)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git ls-files -z -- go.mod **/go.mod
		/repo: git tag --list *
		/repo: git diff --name-only --no-renames -z contrib/envconfig/v1.0.2 -- contrib/envconfig
		/repo: git tag --list contrib/envconfig/v*
	`)
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/3] Fetch main
		      $ git fetch --tags origin main
		[2/3] Inspect checkout
		      Worktree: /repo (including local edits)
		      $ git ls-files -z -- go.mod **/go.mod
		      $ git tag --list *
		      $ git diff --name-only --no-renames -z contrib/envconfig/v1.0.2 -- contrib/envconfig
		[3/3] Lint changed modules
		      $ git tag --list contrib/envconfig/v*
		      contrib/envconfig=1.1.0: ready for pr

		Suggested PR command:
		  yeet pr contrib/envconfig=1.1.0
	`)
}

func TestLintCommit(t *testing.T) {
	commit := "1234567890abcdef1234567890abcdef12345678"
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	changelog := stripIndentation(`
		## [Unreleased]

		### Added

		### Fixed

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix reviewed in the PR.
	`)
	versionGo := stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)
	a, eff := newMockApp(app{commit: "1234567"}, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case `git log -1 "--format=%s (%h)"`:
			return "Prepare release 2026-08-04 (1234567)\n", nil
		case "git ls-files -z -- go.mod **/go.mod":
			return "go.mod\x00", nil
		case "git tag --list *", "git tag --list v*":
			return "v1.47.0\n", nil
		case "git diff --name-only --no-renames -z v1.47.0 -- .":
			return "CHANGELOG.md\x00internal/version.go\x00", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo
	eff.files[filepath.Join(mockRepoRoot, "CHANGELOG.md")] = "Local edits must not be inspected."

	err := a.lint()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], changelog)
	testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], versionGo)
	testEqual(t, eff.files[filepath.Join(mockRepoRoot, "CHANGELOG.md")], "Local edits must not be inspected.")
	if len(eff.files) != 4 {
		t.Fatalf("lint wrote files: %v", eff.files)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git rev-parse --verify --end-of-options 1234567^{commit}
		/repo: git worktree add --detach `+eff.tempDir+` `+commit+`
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git ls-files -z -- go.mod **/go.mod
		`+eff.tempDir+`: git tag --list *
		`+eff.tempDir+`: git diff --name-only --no-renames -z v1.47.0 -- .
		`+eff.tempDir+`: git tag --list v*
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/3] Fetch main
		      $ git fetch --tags origin main
		[2/3] Inspect checkout
		      $ git rev-parse --verify --end-of-options 1234567^{commit}
		      $ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Prepare release 2026-08-04 (1234567)
		      $ git ls-files -z -- go.mod **/go.mod
		      $ git tag --list *
		      $ git diff --name-only --no-renames -z v1.47.0 -- .
		[3/3] Lint changed modules
		      $ git tag --list v*
		      .=1.48.0: ready for release
		      $ git worktree remove --force /tmp/prepare-go-release-123456
	`)
}

func TestLintReportsAllBlockedModules(t *testing.T) {
	a, eff := newMockApp(app{}, func(cmd string) (string, error) {
		switch cmd {
		case "git ls-files -z -- go.mod **/go.mod":
			return "contrib/envconfig/go.mod\x00contrib/tally/go.mod\x00", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "CHANGELOG.md")] = "## [Unreleased]\n"
	eff.files[filepath.Join(mockRepoRoot, "contrib", "envconfig", "go.mod")] = "module go.temporal.io/sdk/contrib/envconfig\n"

	err := a.lint()
	if err == nil || !strings.Contains(err.Error(), "appears to be empty") || !strings.Contains(err.Error(), "contrib/tally/CHANGELOG.md") {
		t.Fatalf("expected both module failures, got %v", err)
	}
	if !strings.Contains(eff.output.String(), "contrib/envconfig=0.1.0: blocked for pr") ||
		!strings.Contains(eff.output.String(), "contrib/tally=0.1.0: blocked for pr") ||
		!strings.Contains(eff.output.String(), "yeet pr contrib/envconfig=0.1.0 contrib/tally=0.1.0") {
		t.Fatalf("missing module reports or suggested command:\n%s", eff.output.String())
	}
}

// Release tags are the only record of a contrib module's current version, so the
// fetch has to bring them along.
func TestFetchMain(t *testing.T) {
	a, eff := newMockApp(app{}, nil)

	err := a.fetchMain()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `/repo: git fetch --tags origin main`)
}

func TestCreateWorktree(t *testing.T) {
	a, eff := newMockApp(app{}, func(cmd string) (string, error) {
		if cmd == `git log -1 "--format=%s (%h)"` {
			return "Initial commit (abc123)\n", nil
		}
		return "", nil
	})

	a.repoRoot = "/foo/bar"

	w, err := a.createWorktree("quux", "origin/main")
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
	if w.worktreeRoot != eff.tempDir {
		t.Fatalf("unexpected worktree root: got %q, want %q", w.worktreeRoot, eff.tempDir)
	}

	err = w.cleanup()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/foo/bar: git worktree add -b quux /tmp/prepare-go-release-123456 origin/main
		/tmp/prepare-go-release-123456: git log -1 "--format=%s (%h)"
		/foo/bar: git worktree remove --force /tmp/prepare-go-release-123456
	`)
}
