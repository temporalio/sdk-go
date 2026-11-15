package main

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

func TestFindNoChanges(t *testing.T) {
	a, eff := newMockApp(app{}, nil, func(cmd string) (string, error) {
		if cmd == "git rev-parse --verify --end-of-options origin/main^{commit}" {
			return strings.Repeat("a", 40), nil
		}
		return "", nil
	})
	c := findApp{app: *a}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), "")
}

func TestPR(t *testing.T) {
	modules := []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}

	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	input["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.47.0"
		)
	`)
	input["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [Unreleased]

		### Fixed

		- A fix.
	`)
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.47.0
	`)
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [Unreleased]

		### Fixed

		- A fix.
	`)

	output := maps.Clone(input)
	output["CHANGELOG.md"] = stripIndentation(`
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
	`) + "\n\n"
	output["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
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
	`) + "\n\n"

	output["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)

	a, eff := newMockPR(prApp{modules: modules}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0 contrib/envconfig=1.0.3" -- CHANGELOG.md internal/version.go contrib/envconfig/CHANGELOG.md`:
			testEqualMaps(t, eff.files, output)
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body "sdk=1.48.0 contrib/envconfig=1.0.3"`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		}
		return "", nil
	}

	err := a.run()
	if err != nil {
		t.Fatal(err)
	}

	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /tmp/prepare-go-release-123456:
		$ git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0 contrib/envconfig=1.0.3" -- CHANGELOG.md internal/version.go contrib/envconfig/CHANGELOG.md

		# Running in /tmp/prepare-go-release-123456:
		$ git push --set-upstream origin chore/release-2026-08-04

		# Running in /tmp/prepare-go-release-123456:
		$ gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body "sdk=1.48.0 contrib/envconfig=1.0.3"
		https://github.com/temporalio/sdk-go/pull/123

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin and create worktree
		[2/3] Update release files
		[3/3] Commit and publish draft PR
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123

		Next steps:
		1. Lint the release branch:
		      publisher lint chore/release-2026-08-04 sdk=1.48.0 contrib/envconfig=1.0.3
		2. Fix errors and mark the PR as "ready for review".
		3. Merge the PR and copy the commit hash.
		4. Create draft github releases:
		      publisher release <YOUR_COMMIT_HASH> sdk=1.48.0 contrib/envconfig=1.0.3

		Alternatively, you can close the PR and delete the branch:
		      gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
	`)
}

func TestPRWorktreeCreationFailed(t *testing.T) {
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}}}}, nil, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		if cmd == "git worktree add -b chore/release-2026-08-04 "+eff.tempDir+" origin/main" {
			return "", errors.New("command failed")
		}
		return "", nil
	}

	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		command failed
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin and create worktree
	`)
	testEqual(t, fmt.Sprint(err), "create worktree: command failed")
}

func TestPRReleaseFilesCannotBeComputed(t *testing.T) {
	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
	`)

	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}}}}, input, nil)

	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin and create worktree
		[2/3] Update release files
	`)
	testEqual(t, fmt.Sprint(err), "read /tmp/prepare-go-release-123456/CHANGELOG.md: file does not exist")
}

func TestPRCommitFailed(t *testing.T) {
	input := make(map[string]string)
	input["CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		### Fixed

		- A fix.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	input["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.47.0"
		)
	`)

	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}}}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case `git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0" -- CHANGELOG.md internal/version.go`:
			return "", errors.New("command failed")
		}
		return "", nil
	})

	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /tmp/prepare-go-release-123456:
		$ git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0" -- CHANGELOG.md internal/version.go
		command failed

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin and create worktree
		[2/3] Update release files
		[3/3] Commit and publish draft PR
	`)
	testEqual(t, fmt.Sprint(err), "commit release files: command failed")
}

func TestPRWorktreeCleanupFailed(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		Invalid changelog
	`)
	a, eff := newMockPR(prApp{
		modules: []pure.VersionedModule{{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}}},
	}, input, func(cmd string) (string, error) {
		if strings.HasPrefix(cmd, "git worktree remove --force ") {
			return "", cleanupErr
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
		cleanup failed
	`)
	testEqual(t, fmt.Sprint(err), `
		/tmp/prepare-go-release-123456/contrib/envconfig/CHANGELOG.md: could not find changelog section for 'Unreleased'
		remove worktree: cleanup failed
	`)
}

func TestLintCommit(t *testing.T) {
	commit := "1234567890abcdef1234567890abcdef12345678"
	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	input["CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		### Added

		### Fixed

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix reviewed in the PR.
	`)
	input["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)
	input["/repo/CHANGELOG.md"] = stripIndentation(`
		Local edits must not be inspected.
	`)
	output := maps.Clone(input)
	a, eff := newMockLint(lintApp{modules: []pure.VersionedModule{{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}}}, commit: "1234567"}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		}
		return "", nil
	})
	err := a.run()
	if err != nil {
		t.Fatal(err)
	}

	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git rev-parse --verify --end-of-options 1234567^{commit}
		`+commit+`

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/2] Fetch origin and create worktree
		[2/2] Validate release files
		      go.temporal.io/sdk: validate go.mod
		      go.temporal.io/sdk: verify that go.mod requirements are published
		      go.temporal.io/sdk: validate version.go
		      go.temporal.io/sdk: validate changelog
		      go.temporal.io/sdk: passed inspection ✔
	`)
}

func TestLintReportsAllBlockedModules(t *testing.T) {
	tally, err := pure.ContribModule("contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]
		## [0.0.1]
	`) + "\n"
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
	`) + "\n"
	a, eff := newMockLint(lintApp{modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}},
		{Module: tally, Version: pure.Version{0, 1, 0}},
	}, commit: "abc123"}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		}
		return "", nil
	})
	err = a.run()
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/2] Fetch origin and create worktree
		[2/2] Validate release files
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk/contrib/tally: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: failed inspection ✘
		      go.temporal.io/sdk/contrib/tally: failed inspection ✘
	`)
	testEqual(t, fmt.Sprint(err), `
		go.temporal.io/sdk/contrib/envconfig: invalid changelog: expected exactly one section for "0.1.0", found 0
		go.temporal.io/sdk/contrib/tally: read /tmp/prepare-go-release-123456/contrib/tally/go.mod: file does not exist
	`)
}

func TestRelease(t *testing.T) {
	commit := "1234567890abcdef1234567890abcdef12345678"
	modules := []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}

	input := make(map[string]string)
	input["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [Unreleased]

		## [1.48.0] - 2026-08-04

		### Fixed

		- An SDK fix.
	`)
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [Unreleased]

		## [1.0.3] - 2026-08-04

		### Added

		- A contrib feature.
	`)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.47.0
	`)
	input["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)
	output := maps.Clone(input)
	output["prepare-release-notes.md"] = stripIndentation(`
		## Highlights

		### Added

		- A contrib feature.
	`) + "\n"
	sdkOutput := maps.Clone(input)
	sdkOutput["prepare-release-notes.md"] = stripIndentation(`
		## Highlights

		### Fixed

		- An SDK fix.
	`) + "\n"

	a, eff := newMockRelease(releaseApp{modules: modules, commit: "1234567"}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case "git tag --list v*":
			return "v1.47.0\n", nil
		case "git tag --list contrib/envconfig/v*":
			return "contrib/envconfig/v1.0.2\n", nil
		case "gh release create v1.48.0 --draft --target " + commit + " --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes":
			testEqualMaps(t, eff.files, sdkOutput)
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk\n", nil
		case "gh release create contrib/envconfig/v1.0.3 --draft --target " + commit + " --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			testEqualMaps(t, eff.files, output)
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib\n", nil
		}
		return "", nil
	}

	err := a.run()
	if err != nil {
		t.Fatal(err)
	}

	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git rev-parse --verify --end-of-options 1234567^{commit}
		`+commit+`

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`

		# Running in /tmp/prepare-go-release-123456:
		$ gh release create v1.48.0 --draft --target `+commit+` --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes
		https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk

		# Running in /tmp/prepare-go-release-123456:
		$ gh release create contrib/envconfig/v1.0.3 --draft --target `+commit+` --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Creating draft releases for sdk=1.48.0 contrib/envconfig=1.0.3

		[1/4] Fetch origin and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk: validate go.mod
		      go.temporal.io/sdk: verify that go.mod requirements are published
		      go.temporal.io/sdk: validate version.go
		      go.temporal.io/sdk: validate changelog
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      Modules passed inspection ✔
		[3/4] Get release notes
		[4/4] Create draft release(s)
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib

		To roll back successful draft releases, run:
		      gh release delete v1.48.0 --yes
		      gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestReleaseLintBeforePublish(t *testing.T) {
	modules := []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}
	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
	`) + "\n"
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
	`) + "\n"
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		## [1.0.3] - 2026-08-04
	`) + "\n"
	input["internal/version.go"] = stripIndentation(`
		SDKVersion = "1.48.0"
	`)
	input["CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix.
	`)
	a, eff := newMockRelease(releaseApp{modules: modules, commit: "abc123"}, input, func(cmd string) (string, error) {
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
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /repo:
		$ git rev-parse --verify --end-of-options abc123^{commit}
		abc123

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 abc123

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, fmt.Sprint(err), `contrib/envconfig/CHANGELOG.md: changelog section for "1.0.3" appears to be empty`)
}

func TestReleaseStillReportsCreatedDraftsOnFailure(t *testing.T) {
	tally, err := pure.ContribModule("contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	modules := []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}},
		{Module: tally, Version: pure.Version{0, 1, 0}},
	}
	input := make(map[string]string)
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
	`) + "\n"
	input["contrib/tally/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/tally
	`) + "\n"
	input["contrib/tally/CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		## [0.1.0] - 2026-08-04

		### Added

		- A feature.
	`)
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		## [Unreleased]

		## [0.1.0] - 2026-08-04

		### Added

		- A feature.
	`)
	a, eff := newMockRelease(releaseApp{modules: modules, commit: "abc123"}, input, func(cmd string) (string, error) {
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
	err = a.run()
	testEqual(t, eff.output.String(), `
		Creating draft releases for contrib/envconfig=0.1.0 contrib/tally=0.1.0

		[1/4] Fetch origin and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk/contrib/tally: validate go.mod
		      go.temporal.io/sdk/contrib/tally: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/tally: validate changelog
		      Modules passed inspection ✔
		[3/4] Get release notes
		[4/4] Create draft release(s)
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-envconfig

		To roll back successful draft releases, run:
		      gh release delete contrib/envconfig/v0.1.0 --yes
	`)
	testEqual(t, fmt.Sprint(err), "create draft release contrib/tally/v0.1.0: command failed")
}

func TestNewCommand(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantDirs []string
		wantErr  string
	}{
		{name: "lint commit", args: []string{"lint", "abc123", "sdk=1.48.0"}},
		{name: "SDK PR", args: []string{"pr", "sdk=1.48.0"}},
		{name: "contrib PR", args: []string{"pr", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "nested contrib module", args: []string{"pr", "contrib/aws/s3driver/awssdkv2=0.2.2"}, wantDirs: []string{"contrib/aws/s3driver/awssdkv2"}},
		{name: "trailing slash", args: []string{"pr", "contrib/envconfig/=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "SDK release", args: []string{"release", "abc123", "sdk=1.48.0"}},
		{name: "contrib release", args: []string{"release", "abc123", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "unknown command", args: []string{"unknown"}, wantErr: "unknown command \"unknown\"\nusage: publisher (find|pr|lint|release)"},
		{name: "old invocation", args: []string{"1.48.0"}, wantErr: "unknown command \"1.48.0\"\nusage: publisher (find|pr|lint|release)"},
		{name: "multiple PR assignments", args: []string{"pr", "sdk=1.48.0", "contrib/envconfig=1.0.3"}, wantDirs: []string{"", "contrib/envconfig"}},
		{name: "multiple release assignments", args: []string{"release", "abc123", "sdk=1.48.0", "contrib/envconfig=1.0.3"}, wantDirs: []string{"", "contrib/envconfig"}},
		{name: "duplicate SDK", args: []string{"pr", "sdk=1.48.0", "sdk=1.48.1"}, wantErr: `module "go.temporal.io/sdk" is specified more than once`},
		{name: "duplicate contrib", args: []string{"pr", "contrib/envconfig=1.0.3", "./contrib/envconfig/=1.1.0"}, wantErr: `module "go.temporal.io/sdk/contrib/envconfig" is specified more than once`},
		{name: "duplicate release module", args: []string{"release", "abc123", "sdk=1.48.0", "sdk=1.48.0"}, wantErr: `module "go.temporal.io/sdk" is specified more than once`},
		{name: "invalid second version", args: []string{"pr", "sdk=1.48.0", "contrib/envconfig=bad"}, wantErr: `invalid version "bad"; expected a version like '1.48.0'`},
		{name: "release kind", args: []string{"pr", "sdk=minor"}, wantErr: `invalid version "minor"; expected a version like '1.48.0'`},
		{name: "non-contrib module", args: []string{"pr", "internal=1.0.3"}, wantErr: `invalid module "internal"; expected a slash-separated contrib module directory such as 'contrib/envconfig'`},
		{name: "escaping module", args: []string{"pr", "contrib/../internal=1.0.3"}, wantErr: `invalid module "contrib/../internal"; expected a slash-separated contrib module directory such as 'contrib/envconfig'`},
		{name: "missing equals", args: []string{"pr", "contrib/envconfig"}, wantErr: `invalid release "contrib/envconfig"; expected MODULE=VERSION, such as 'sdk=1.48.0'`},
		{name: "empty module", args: []string{"pr", "=1.0.3"}, wantErr: `invalid module ""; expected a slash-separated contrib module directory such as 'contrib/envconfig'`},
		{name: "empty version", args: []string{"pr", "contrib/envconfig="}, wantErr: `invalid version ""; expected a version like '1.48.0'`},
		{name: "extra equals", args: []string{"pr", "sdk=1.48.0=1.49.0"}, wantErr: `invalid version "1.48.0=1.49.0"; expected a version like '1.48.0'`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := newCommand(&mockEffects{}, test.args)
			if test.wantErr != "" {
				testEqual(t, fmt.Sprint(err), test.wantErr)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var name, commit string
			var modules []pure.VersionedModule
			switch got := got.(type) {
			case *lintApp:
				name, commit, modules = "lint", got.commit, got.modules
			case *prApp:
				name, modules = "pr", got.modules
			case *releaseApp:
				name, commit, modules = "release", got.commit, got.modules
			default:
				t.Fatalf("unexpected command type: %T", got)
			}
			if name != test.args[0] {
				t.Fatalf("unexpected command: got %q, want %q", name, test.args[0])
			}
			if (name == "release" || name == "lint") && commit != "abc123" {
				t.Fatalf("unexpected commit: %q", commit)
			}
			wantDirs := test.wantDirs
			if wantDirs == nil {
				wantDirs = []string{""}
			}
			if len(modules) != len(wantDirs) {
				t.Fatalf("unexpected module count: got %d, want %d", len(modules), len(wantDirs))
			}
			for i, module := range modules {
				if gotDir := module.Module.Dir; gotDir != wantDirs[i] {
					t.Fatalf("unexpected module directory: got %q, want %q", gotDir, wantDirs[i])
				}
			}

		})
	}
}
