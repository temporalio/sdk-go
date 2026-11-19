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
		{name: "invalid date", args: []string{"--release-date", "August 4", "1.48.0"}, wantErr: "invalid release date"},
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
			if gotDir := got.target.dir; gotDir != test.wantDir {
				t.Fatalf("unexpected module directory: got %q, want %q", gotDir, test.wantDir)
			}

		})
	}
}

func TestPrepareEverything(t *testing.T) {
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

	a, eff := newMockApp(app{target: sdkTarget(), version: version, releaseDate: date}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list v*":
			return "v1.46.0\nv1.47.0\n", nil
		case `git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")], updatedChangelog)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "go.mod")], goMod)
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "internal", "version.go")], updatedVersionGo)
		case `gh pr create --draft --base main --head chore/release-1.48.0 --label skip-changelog --title "Prepare release 1.48.0" --body "Prepare release 1.48.0"`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case `gh release create v1.48.0 --draft --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes`:
			testEqual(t, eff.files[filepath.Join(eff.tempDir, "prepare-release-notes.md")], "## Highlights\n\n### Fixed\n\n- A fix.\n")
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-abc\n", nil
		}
		return "", nil
	}

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	// TESTS

	err := a.prepareRelease()
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
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-1.48.0 --label skip-changelog --title "Prepare release 1.48.0" --body "Prepare release 1.48.0"
		`+eff.tempDir+`: gh release create v1.48.0 --draft --title v1.48.0 --notes-file `+eff.tempDir+`/prepare-release-notes.md --generate-notes
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing go.temporal.io/sdk 1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
		[4/5] Publish draft PR and draft release
		      $ git push --set-upstream origin chore/release-1.48.0
		      $ gh pr create --draft --base main --head chore/release-1.48.0 --label skip-changelog --title "Prepare release 1.48.0" --body "Prepare release 1.48.0"
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create v1.48.0 --draft --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		[5/5] Clean up release worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.

		To roll back this release, close the PR and delete the draft release:
		  gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
		  gh release delete v1.48.0 --yes
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

	a, eff := newMockApp(app{target: target, version: semver{1, 0, 3}, releaseDate: date}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.0\ncontrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.1\n", nil
		case `git commit -m "Prepare contrib/envconfig release 1.0.3" -- contrib/envconfig/CHANGELOG.md`:
			testEqual(t, eff.files[changelogPath], updatedChangelog)
			testEqual(t, eff.files[goModPath], goMod)
		case `gh pr create --draft --base main --head chore/release-contrib-envconfig-1.0.3 --label skip-changelog --title "Prepare contrib/envconfig release 1.0.3" --body "Prepare contrib/envconfig release 1.0.3"`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		case `gh release create contrib/envconfig/v1.0.3 --draft --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false`:
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

	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-contrib-envconfig-1.0.3 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list contrib/envconfig/v*
		`+eff.tempDir+`: git commit -m "Prepare contrib/envconfig release 1.0.3" -- contrib/envconfig/CHANGELOG.md
		`+eff.tempDir+`: git push --set-upstream origin chore/release-contrib-envconfig-1.0.3
		`+eff.tempDir+`: gh pr create --draft --base main --head chore/release-contrib-envconfig-1.0.3 --label skip-changelog --title "Prepare contrib/envconfig release 1.0.3" --body "Prepare contrib/envconfig release 1.0.3"
		`+eff.tempDir+`: gh release create contrib/envconfig/v1.0.3 --draft --title contrib/envconfig/v1.0.3 --notes-file `+eff.tempDir+`/prepare-release-notes.md --latest=false
		/repo: git worktree remove --force `+eff.tempDir,
	)
	testEqual(t, eff.output.String(), `
		Preparing go.temporal.io/sdk/contrib/envconfig 1.0.3

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-contrib-envconfig-1.0.3 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list contrib/envconfig/v*
		[3/5] Commit release files
		      $ git commit -m "Prepare contrib/envconfig release 1.0.3" -- contrib/envconfig/CHANGELOG.md
		[4/5] Publish draft PR and draft release
		      $ git push --set-upstream origin chore/release-contrib-envconfig-1.0.3
		      $ gh pr create --draft --base main --head chore/release-contrib-envconfig-1.0.3 --label skip-changelog --title "Prepare contrib/envconfig release 1.0.3" --body "Prepare contrib/envconfig release 1.0.3"
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123
		      Release notes: /tmp/prepare-go-release-123456/prepare-release-notes.md
		      $ gh release create contrib/envconfig/v1.0.3 --draft --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-abc
		[5/5] Clean up release worktree
		      $ git worktree remove --force /tmp/prepare-go-release-123456
		      Done.

		To roll back this release, close the PR and delete the draft release:
		  gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
		  gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestPrepareReleaseStopsBeforePush(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 2, 3}, stopBeforePush: true}, func(cmd string) (string, error) {
		if cmd == "git tag --list v*" {
			return "v1.2.2\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(mockTempDir, "go.mod")] = "module go.temporal.io/sdk\n"
	eff.files[filepath.Join(mockTempDir, "CHANGELOG.md")] = stripIndentation(`
		# Changelog

		## [Unreleased]

		### Fixed

		- A fix.
	`)
	eff.files[filepath.Join(mockTempDir, "internal", "version.go")] = `SDKVersion = "1.2.2"` + "\n"

	err := a.prepareRelease()
	if err == nil || !strings.Contains(err.Error(), "--stop-before-push") {
		t.Fatalf("expected stop-before-push error, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-1.2.3 `+eff.tempDir+` origin/main
		`+eff.tempDir+`: git log -1 "--format=%s (%h)"
		`+eff.tempDir+`: git tag --list v*
		`+eff.tempDir+`: git commit -m "Prepare release 1.2.3" -- CHANGELOG.md internal/version.go
	`)
}

func TestPrepareReleaseLeavesDirectoryIfWorktreeCreationFails(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		if cmd == "git worktree add -b chore/release-1.48.0 "+eff.tempDir+" origin/main" {
			return "", errors.New("command failed")
		}
		return "", nil
	}

	err := a.prepareRelease()
	if err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("expected worktree creation failure, got %v", err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git worktree add -b chore/release-1.48.0 `+eff.tempDir+` origin/main
	`)
	testEqual(t, eff.output.String(), `
		Preparing go.temporal.io/sdk 1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
	`)
}

func TestPrepareReleaseValidatesBeforePushingBranch(t *testing.T) {
	goMod := stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578
	`)

	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		if cmd == `git log -1 "--format=%s (%h)"` {
			return "Initial commit (abc123)\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod

	err := a.prepareRelease()
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
		Preparing go.temporal.io/sdk 1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
	`)
}

func TestPrepareReleaseLeavesWorktreeAfterFailure(t *testing.T) {
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

	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}, releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)}, func(cmd string) (string, error) {
		switch cmd {
		case `git log -1 "--format=%s (%h)"`:
			return "Initial commit (abc123)\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case `git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`:
			return "", errors.New("command failed")
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = changelog
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := a.prepareRelease()
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
		Preparing go.temporal.io/sdk 1.48.0

		[1/5] Fetch main
		      $ git fetch --tags origin main
		[2/5] Create release worktree
		      $ git worktree add -b chore/release-1.48.0 /tmp/prepare-go-release-123456 origin/main
		      $ git log -1 "--format=%s (%h)"
		      Worktree: /tmp/prepare-go-release-123456
		      HEAD: Initial commit (abc123)
		      $ git tag --list v*
		[3/5] Commit release files
		      $ git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go
	`)
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

	w, err := a.createWorktree("quux")
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
