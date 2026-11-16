package main

import (
	"errors"
	"fmt"
	"io"
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

func TestFindReleasableModules(t *testing.T) {
	a, eff := newMockApp(app{}, nil, func(cmd string) (string, error) {
		switch {
		case strings.HasPrefix(cmd, "git rev-parse"):
			return strings.Repeat("a", 40), nil
		case strings.HasPrefix(cmd, "git log"):
			return strings.Repeat("b", 40), nil
		default:
			return "", nil
		}
	})
	paths := []string{
		"CHANGELOG.md",
		"contrib/envconfig/go.mod",
		"contrib/envconfig/CHANGELOG.md",
		"contrib/envconfig/test/go.mod",
		"contrib/fragments/go.mod",
		"contrib/fragments/changelog/added/new-module.md",
		"contrib/test-only/go.mod",
	}
	a.contribModules = pure.ContribModules(paths)
	eff.input = "mmm"
	c := findApp{app: *a}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	output := eff.output.String()
	if !strings.Contains(output, "publisher pr sdk=0.1.0 contrib/envconfig=0.1.0 contrib/fragments=0.1.0") {
		t.Fatalf("missing release command for eligible modules:\n%s", output)
	}
	if strings.Contains(output, "contrib/envconfig/test") || strings.Contains(output, "contrib/test-only") {
		t.Fatalf("offered a module without a changelog:\n%s", output)
	}
	if eff.input != "" {
		t.Fatalf("not all eligible modules were offered: unused input %q", eff.input)
	}
	if strings.Contains(eff.audit.String(), ":(exclude,literal)contrib/envconfig/test/") {
		t.Fatalf("nested test module was excluded from parent changes:\n%s", eff.audit.String())
	}
}

func TestFindChooseVersion(t *testing.T) {
	latest := pure.Version{1, 48, 0}
	for _, test := range []struct {
		name    string
		input   string
		want    *pure.Version
		wantEOF bool
	}{
		{name: "minor", input: "m", want: &pure.Version{1, 49, 0}},
		{name: "patch", input: "p", want: &pure.Version{1, 48, 1}},
		{name: "skip", input: "s"},
		{name: "enter", input: "\n"},
		{name: "carriage return", input: "\r"},
		{name: "invalid then enter", input: "x\n"},
		{name: "EOF", wantEOF: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, eff := newMockApp(app{}, nil, nil)
			eff.input = test.input
			c := findApp{app: *a}
			got, err := c.chooseVersion(pure.SDKModule(), &latest, "HEAD")
			if test.wantEOF {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("expected EOF, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if got != nil {
					t.Fatalf("selected version %s, want a skip", got)
				}
			} else if got == nil || *got != *test.want {
				t.Fatalf("selected version %v, want %s", got, test.want)
			}
		})
	}
}

func TestPR(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
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
	input["CHANGELOG.md"] = "# Changelog\n"
	input["changelog/fixed/dancing-duck.md"] = "An SDK fix.\n"
	input["contrib/envconfig/go.mod"] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.47.0
	`)
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n"
	input["contrib/envconfig/changelog/added/singing-swan.md"] = "A contrib feature.\n"
	input["contrib/tally/changelog/fixed/patient-panda.md"] = "An unrelated fix.\n"

	output := maps.Clone(input)
	output["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04

		### Fixed

		- An SDK fix.
	`) + "\n"
	output["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.0.3] - 2026-08-04

		### Added

		- A contrib feature.
	`) + "\n"
	output["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)
	delete(output, "changelog/fixed/dancing-duck.md")
	delete(output, "contrib/envconfig/changelog/added/singing-swan.md")

	a, eff := newMockPR(prApp{modules: modules}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse HEAD":
			return strings.TrimSpace(sdkRustRevision), nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo " + mockTempDir + " --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md --fragments changelog":
			testEqual(t, eff.files["changelog/fixed/dancing-duck.md"], input["changelog/fixed/dancing-duck.md"])
			eff.files["CHANGELOG.md"] = output["CHANGELOG.md"]
			delete(eff.files, "changelog/fixed/dancing-duck.md")
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo " + mockTempDir + " --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog":
			testEqual(t, eff.files["contrib/envconfig/changelog/added/singing-swan.md"], input["contrib/envconfig/changelog/added/singing-swan.md"])
			eff.files["contrib/envconfig/CHANGELOG.md"] = output["contrib/envconfig/CHANGELOG.md"]
			delete(eff.files, "contrib/envconfig/changelog/added/singing-swan.md")
		case "git diff --name-only --diff-filter=D -z -- changelog/":
			return "changelog/fixed/dancing-duck.md\x00", nil
		case "git diff --name-only --diff-filter=D -z -- contrib/envconfig/changelog/":
			return "contrib/envconfig/changelog/added/singing-swan.md\x00", nil
		case `git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0 contrib/envconfig=1.0.3"`:
			testEqualMaps(t, eff.files, output)
		case `gh pr create --draft --base main --head chore/release-2026-08-04 --label skip-changelog --title "Prepare release 2026-08-04" --body "sdk=1.48.0 contrib/envconfig=1.0.3"`:
			return "https://github.com/temporalio/sdk-go/pull/123\n", nil
		}
		return "", nil
	}
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md --fragments changelog

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog

		# Running in /tmp/prepare-go-release-123456:
		$ git diff --name-only --diff-filter=D -z -- changelog/
		changelog/fixed/dancing-duck.md`+"\x00"+`

		# Running in /tmp/prepare-go-release-123456:
		$ git diff --name-only --diff-filter=D -z -- contrib/envconfig/changelog/
		contrib/envconfig/changelog/added/singing-swan.md`+"\x00"+`

		# Running in /tmp/prepare-go-release-123456:
		$ git add -A -- CHANGELOG.md internal/version.go changelog/fixed/dancing-duck.md contrib/envconfig/CHANGELOG.md contrib/envconfig/changelog/added/singing-swan.md

		# Running in /tmp/prepare-go-release-123456:
		$ git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0 contrib/envconfig=1.0.3"

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

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
		[3/3] Commit and publish draft PR
		      Draft PR: https://github.com/temporalio/sdk-go/pull/123

		Next steps:
		1. Lint the release branch:
		      publisher lint-release chore/release-2026-08-04 sdk=1.48.0 contrib/envconfig=1.0.3
		2. Fix errors and mark the PR as "ready for review".
		3. Merge the PR and copy the commit hash.
		4. Create draft github releases:
		      publisher release <YOUR_COMMIT_HASH> sdk=1.48.0 contrib/envconfig=1.0.3

		Alternatively, you can close the PR and delete the branch:
		      gh pr close https://github.com/temporalio/sdk-go/pull/123 --delete-branch
	`)
}

func TestCoreToolSetupFailure(t *testing.T) {
	modules := []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}
	for _, name := range []string{"pr", "lint-release", "release"} {
		t.Run(name, func(t *testing.T) {
			a, eff := newMockApp(app{}, nil, nil)
			eff.commandHandler = func(cmd string) (string, error) {
				if cmd == "git rev-parse HEAD" {
					return "wrong-revision\n", nil
				}
				return "", nil
			}
			var c command
			switch name {
			case "pr":
				c = &prApp{app: *a, modules: modules}
			case "lint-release":
				c = &lintReleaseApp{app: *a, modules: modules, commit: "abc123"}
			case "release":
				c = &releaseApp{app: *a, modules: modules, commit: "abc123"}
			}
			err := c.run()
			if err == nil || !strings.Contains(err.Error(), "wrong-revision") {
				t.Fatalf("expected a tool setup error, got %v", err)
			}
			audit := eff.audit.String()
			if count := strings.Count(audit, "$ git rev-parse HEAD\n"); count != 1 {
				t.Fatalf("verified the tool checkout %d times, want 1", count)
			}
			if strings.Contains(audit, "git worktree add") || strings.Contains(audit, "cargo ") {
				t.Fatalf("started release work after a tool setup failure:\n%s", audit)
			}
			if strings.Contains(eff.output.String(), "validate go.mod") {
				t.Fatal("reported a global setup failure during per-module validation")
			}
		})
	}
}

func TestPRWorktreeCreationFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
	}}, nil, func(cmd string) (string, error) {
		if cmd == "git worktree add -b chore/release-2026-08-04 "+mockTempDir+" origin/main" {
			return "", errors.New("command failed")
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main
		command failed
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
	`)
	testEqual(t, fmt.Sprint(err), "create worktree: command failed")
	testEqualMaps(t, eff.files, map[string]string{})
}

func TestPRReleaseFilesCannotBeComputed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["go.mod"] = "module go.temporal.io/sdk\n"
	input["CHANGELOG.md"] = "# Changelog\n"
	input["changelog/fixed/dancing-duck.md"] = "A fix.\n"
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
	}}, input, nil)
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
	`)
	testEqual(t, fmt.Sprint(err), "read /tmp/prepare-go-release-123456/internal/version.go: file does not exist")
	testEqualMaps(t, eff.files, input)
}

func TestPRCommitFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["go.mod"] = "module go.temporal.io/sdk\nrequire go.temporal.io/api v1.63.4\n"
	input["internal/version.go"] = "const (\n\tSDKVersion = \"1.47.0\"\n)"
	input["changelog/fixed/dancing-duck.md"] = "A fix.\n"
	input["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`) + "\n"
	output := maps.Clone(input)
	output["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix.

		## [1.47.0] - 2026-07-28

		### Added

		- A previous feature.
	`) + "\n"
	output["internal/version.go"] = "const (\n\tSDKVersion = \"1.48.0\"\n)"
	delete(output, "changelog/fixed/dancing-duck.md")
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
	}}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse HEAD":
			return strings.TrimSpace(sdkRustRevision), nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo " + mockTempDir + " --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md --fragments changelog":
			eff.files["CHANGELOG.md"] = output["CHANGELOG.md"]
			delete(eff.files, "changelog/fixed/dancing-duck.md")
		case "git diff --name-only --diff-filter=D -z -- changelog/":
			return "changelog/fixed/dancing-duck.md\x00", nil
		case `git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0"`:
			testEqualMaps(t, eff.files, output)
			return "", errors.New("command failed")
		}
		return "", nil
	}
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md --fragments changelog

		# Running in /tmp/prepare-go-release-123456:
		$ git diff --name-only --diff-filter=D -z -- changelog/
		changelog/fixed/dancing-duck.md`+"\x00"+`

		# Running in /tmp/prepare-go-release-123456:
		$ git add -A -- CHANGELOG.md internal/version.go changelog/fixed/dancing-duck.md

		# Running in /tmp/prepare-go-release-123456:
		$ git commit -m "Prepare release 2026-08-04\n\nsdk=1.48.0"
		command failed

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
		[3/3] Commit and publish draft PR
	`)
	testEqual(t, fmt.Sprint(err), "commit release files: command failed")
	testEqualMaps(t, eff.files, output)
}

func TestPRWorktreeCleanupFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n"
	input["contrib/envconfig/changelog/fixed/empty-otter.md"] = ""
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}},
	}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo " + mockTempDir + " --version 0.1.0 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog":
			return "", errors.New("empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md")
		case "git worktree remove --force " + mockTempDir:
			return "", errors.New("cleanup failed")
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 0.1.0 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog
		empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
		cleanup failed
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
	`)
	testEqual(t, fmt.Sprint(err), `
		changelog-tool prepare: empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md
		remove worktree: cleanup failed
	`)
	testEqualMaps(t, eff.files, input)
}

func TestPRChangelogFragmentValidationFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n"
	input["contrib/envconfig/changelog/fixed/empty-otter.md"] = ""
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}}, input, func(cmd string) (string, error) {
		if cmd == "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo "+mockTempDir+" --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog" {
			return "", errors.New("empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md")
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, fmt.Sprint(err), "changelog-tool prepare: empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md")
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog
		empty fragment: /tmp/prepare-go-release-123456/contrib/envconfig/changelog/fixed/empty-otter.md

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
	`)
	testEqualMaps(t, eff.files, input)
}

func TestPRChangelogPreparationFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n"
	input["contrib/envconfig/changelog/fixed/dancing-duck.md"] = "A fix.\n"
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}}, input, func(cmd string) (string, error) {
		if cmd == "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo "+mockTempDir+" --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog" {
			return "", errors.New("command failed")
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, fmt.Sprint(err), "changelog-tool prepare: command failed")
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog
		command failed

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
	`)
	testEqualMaps(t, eff.files, input)
}

func TestPRStagingFailed(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n"
	input["contrib/envconfig/changelog/fixed/dancing-duck.md"] = "A fix.\n"
	output := maps.Clone(input)
	output["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n\n## [1.0.3] - 2026-08-04\n\n### Fixed\n\n- A fix.\n"
	delete(output, "contrib/envconfig/changelog/fixed/dancing-duck.md")
	a, eff := newMockPR(prApp{modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse HEAD":
			return strings.TrimSpace(sdkRustRevision), nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo " + mockTempDir + " --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog":
			eff.files["contrib/envconfig/CHANGELOG.md"] = output["contrib/envconfig/CHANGELOG.md"]
			delete(eff.files, "contrib/envconfig/changelog/fixed/dancing-duck.md")
		case "git diff --name-only --diff-filter=D -z -- contrib/envconfig/changelog/":
			return "contrib/envconfig/changelog/fixed/dancing-duck.md\x00", nil
		case "git add -A -- contrib/envconfig/CHANGELOG.md contrib/envconfig/changelog/fixed/dancing-duck.md":
			return "", errors.New("command failed")
		}
		return "", nil
	}
	err := a.run()
	testEqual(t, fmt.Sprint(err), "stage release files: command failed")
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git worktree add -b chore/release-2026-08-04 /tmp/prepare-go-release-123456 origin/main

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- prepare --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md --fragments contrib/envconfig/changelog

		# Running in /tmp/prepare-go-release-123456:
		$ git diff --name-only --diff-filter=D -z -- contrib/envconfig/changelog/
		contrib/envconfig/changelog/fixed/dancing-duck.md`+"\x00"+`

		# Running in /tmp/prepare-go-release-123456:
		$ git add -A -- contrib/envconfig/CHANGELOG.md contrib/envconfig/changelog/fixed/dancing-duck.md
		command failed

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Preparing release PR

		[1/3] Fetch origin, prepare changelog tool, and create worktree
		[2/3] Update release files
		[3/3] Commit and publish draft PR
	`)
	testEqualMaps(t, eff.files, output)
}

func TestLintRequiresFragmentsPerChangedModule(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["/repo/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04
	`)
	input["/repo/contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.0.3] - 2026-08-04
	`)
	input["/repo/contrib/envconfig/envconfig.go"] = stripIndentation(`
		package envconfig

		const NewOption = true
	`)
	input["/repo/contrib/envconfig/changelog/added/helpful-hippo.md"] = stripIndentation(`
		Add a new configuration option.
	`)
	input["/repo/contrib/fragments/feature.go"] = stripIndentation(`
		package fragments

		const NewFeature = true
	`)
	input["/repo/contrib/fragments/changelog/README.md"] = stripIndentation(`
		Add pending release notes to category directories.
	`)
	output := maps.Clone(input)
	a, eff := newMockApp(app{}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options base^{commit}":
			return "base-sha\n", nil
		case "git diff --name-only --no-renames base-sha HEAD -- contrib/envconfig/ :(exclude,glob)contrib/envconfig/**/go.mod :(exclude,glob)contrib/envconfig/**/go.sum":
			return "contrib/envconfig/envconfig.go\ncontrib/envconfig/changelog/added/helpful-hippo.md\n", nil
		case "git diff --name-only --no-renames base-sha HEAD -- contrib/fragments/ :(exclude,glob)contrib/fragments/**/go.mod :(exclude,glob)contrib/fragments/**/go.sum":
			return "contrib/fragments/feature.go\n", nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments contrib/fragments/changelog --base base-sha --require-new":
			return "", errors.New("add a new changelog fragment")
		}
		return "", nil
	})
	a.contribModules = []pure.Module{contribEnvconfig(t), {Dir: "contrib/fragments"}}
	c := lintApp{app: *a, baseCommit: "base"}
	err := c.run()
	testEqual(t, fmt.Sprint(err), "check changelog for contrib/fragments: changelog-tool check: add a new changelog fragment")
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git rev-parse --verify --end-of-options base^{commit}
		base-sha

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git diff --name-only --no-renames base-sha HEAD -- ./ :(exclude,glob)**/go.mod :(exclude,glob)**/go.sum :(exclude,literal)contrib/envconfig/ :(exclude,literal)contrib/fragments/

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments changelog --base base-sha --changelog CHANGELOG.md

		# Running in /repo:
		$ git diff --name-only --no-renames base-sha HEAD -- contrib/envconfig/ :(exclude,glob)contrib/envconfig/**/go.mod :(exclude,glob)contrib/envconfig/**/go.sum
		contrib/envconfig/envconfig.go
		contrib/envconfig/changelog/added/helpful-hippo.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments contrib/envconfig/changelog --base base-sha --require-new --changelog contrib/envconfig/CHANGELOG.md

		# Running in /repo:
		$ git diff --name-only --no-renames base-sha HEAD -- contrib/fragments/ :(exclude,glob)contrib/fragments/**/go.mod :(exclude,glob)contrib/fragments/**/go.sum
		contrib/fragments/feature.go

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments contrib/fragments/changelog --base base-sha --require-new
		add a new changelog fragment
	`)
	testEqual(t, eff.output.String(), "")
	testEqualMaps(t, eff.files, output)
}

func TestLintSkipChangelog(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["/repo/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04
	`)
	input["/repo/contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.0.3] - 2026-08-04
	`)
	output := maps.Clone(input)
	a, eff := newMockApp(app{}, input, func(cmd string) (string, error) {
		if cmd == "git rev-parse --verify --end-of-options base^{commit}" {
			return "base-sha\n", nil
		}
		return "", nil
	})
	a.contribModules = []pure.Module{contribEnvconfig(t)}
	c := lintApp{app: *a, baseCommit: "base", skipChangelog: true}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git rev-parse --verify --end-of-options base^{commit}
		base-sha

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments changelog --base base-sha --changelog CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- check --repo /repo --fragments contrib/envconfig/changelog --base base-sha --changelog contrib/envconfig/CHANGELOG.md
	`)
	testEqual(t, eff.output.String(), "")
	testEqualMaps(t, eff.files, output)
}

func TestLintReleaseCommit(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	commit := "1234567890abcdef1234567890abcdef12345678"
	input := make(map[string]string)
	input["go.mod"] = stripIndentation(`
		module go.temporal.io/sdk
		require go.temporal.io/api v1.63.4
	`)
	input["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04

		### Fixed

		- A fix reviewed in the PR.
	`) + "\n"
	input["internal/version.go"] = stripIndentation(`
		const (
			SDKVersion = "1.48.0"
		)
	`)
	input["/repo/CHANGELOG.md"] = "Local edits must not be inspected.\n"
	input["changelog/fixed/late-llama.md"] = "A fix for the next release.\n"
	output := maps.Clone(input)
	a, eff := newMockLintRelease(lintReleaseApp{commit: "1234567", modules: []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
	}}, input, func(cmd string) (string, error) {
		if cmd == "git rev-parse --verify --end-of-options 1234567^{commit}" {
			return commit + "\n", nil
		}
		return "", nil
	})
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options 1234567^{commit}
		`+commit+`

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/2] Fetch origin, prepare changelog tool, and create worktree
		[2/2] Validate release files
		      go.temporal.io/sdk: validate go.mod
		      go.temporal.io/sdk: verify that go.mod requirements are published
		      go.temporal.io/sdk: validate version.go
		      go.temporal.io/sdk: validate changelog
		      go.temporal.io/sdk: passed inspection ✔
	`)
}

func TestLintReleaseReportsAllBlockedModules(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	tally, err := pure.ContribModule("contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	input := make(map[string]string)
	input["contrib/envconfig/go.mod"] = "module go.temporal.io/sdk/contrib/envconfig\n"
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n\n## [0.0.1] - 2026-07-28\n"
	a, eff := newMockLintRelease(lintReleaseApp{commit: "abc123", modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}},
		{Module: tally, Version: pure.Version{0, 1, 0}},
	}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 0.1.0 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md":
			return "", errors.New("missing release section for 0.1.0")
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		}
		return "", nil
	})
	err = a.run()
	testEqual(t, eff.output.String(), `
		Checking release readiness

		[1/2] Fetch origin, prepare changelog tool, and create worktree
		[2/2] Validate release files
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk/contrib/tally: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: failed inspection ✘
		      go.temporal.io/sdk/contrib/tally: failed inspection ✘
	`)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options abc123^{commit}
		abc123

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 abc123

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 0.1.0 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md
		missing release section for 0.1.0

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, fmt.Sprint(err), `
		go.temporal.io/sdk/contrib/envconfig: changelog-tool release-notes: missing release section for 0.1.0
		go.temporal.io/sdk/contrib/tally: read /tmp/prepare-go-release-123456/contrib/tally/go.mod: file does not exist
	`)
	testEqualMaps(t, eff.files, input)
}

func TestRelease(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	commit := "1234567890abcdef1234567890abcdef12345678"
	modules := []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}
	input := make(map[string]string)
	input["CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.48.0] - 2026-08-04

		### Fixed

		- An SDK fix.
	`) + "\n"
	input["contrib/envconfig/CHANGELOG.md"] = stripIndentation(`
		# Changelog

		## [1.0.3] - 2026-08-04

		### Added

		- A contrib feature.
	`) + "\n"
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
	input["changelog/fixed/late-llama.md"] = "A fix for the next release.\n"
	sdkNotes := stripIndentation(`
		## Notable Changes

		### Fixed

		- An SDK fix.
	`) + "\n"
	contribNotes := stripIndentation(`
		## Notable Changes

		### Added

		- A contrib feature.
	`) + "\n"
	output := maps.Clone(input)
	output["prepare-release-notes.md"] = contribNotes
	sdkOutput := maps.Clone(input)
	sdkOutput["prepare-release-notes.md"] = sdkNotes

	a, eff := newMockRelease(releaseApp{modules: modules, commit: "1234567"}, input, nil)
	eff.commandHandler = func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse HEAD":
			return strings.TrimSpace(sdkRustRevision), nil
		case "git rev-parse --verify --end-of-options 1234567^{commit}":
			return commit + "\n", nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 1.48.0 --changelog CHANGELOG.md":
			return sdkNotes, nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 1.0.3 --changelog contrib/envconfig/CHANGELOG.md":
			return contribNotes, nil
		case "gh release create v1.48.0 --draft --target " + commit + " --title v1.48.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --generate-notes":
			testEqualMaps(t, eff.files, sdkOutput)
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk\n", nil
		case "gh release create contrib/envconfig/v1.0.3 --draft --target " + commit + " --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			testEqualMaps(t, eff.files, output)
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib\n", nil
		}
		return "", nil
	}
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options 1234567^{commit}
		`+commit+`

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 `+commit+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.48.0 --changelog CHANGELOG.md
		## Notable Changes

		### Fixed

		- An SDK fix.

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.0.3 --changelog contrib/envconfig/CHANGELOG.md
		## Notable Changes

		### Added

		- A contrib feature.

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

		[1/4] Fetch origin, prepare changelog tool, and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk: validate go.mod
		      go.temporal.io/sdk: verify that go.mod requirements are published
		      go.temporal.io/sdk: validate version.go
		      go.temporal.io/sdk: validate changelog
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk: passed inspection ✔
		      go.temporal.io/sdk/contrib/envconfig: passed inspection ✔
		[3/4] Get release notes
		[4/4] Create draft release(s)
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-sdk
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/untagged-contrib

		To roll back successful draft releases, run:
		      gh release delete v1.48.0 --yes
		      gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestReleaseEmpty(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["contrib/envconfig/go.mod"] = "module go.temporal.io/sdk/contrib/envconfig\n"
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n\n## [1.0.3] - 2026-08-04\n"
	input["contrib/envconfig/changelog/fixed/late-llama.md"] = "A fix for the next release.\n"
	output := maps.Clone(input)
	output["prepare-release-notes.md"] = "## Notable Changes\n\n"
	a, eff := newMockRelease(releaseApp{commit: "abc123", modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 1.0.3 --changelog contrib/envconfig/CHANGELOG.md":
			return "## Notable Changes\n\n", nil
		case "gh release create contrib/envconfig/v1.0.3 --draft --target abc123 --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "https://github.com/temporalio/sdk-go/releases/tag/draft\n", nil
		}
		return "", nil
	})
	if err := a.run(); err != nil {
		t.Fatal(err)
	}
	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options abc123^{commit}
		abc123

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 abc123

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.0.3 --changelog contrib/envconfig/CHANGELOG.md
		## Notable Changes

		# Running in /tmp/prepare-go-release-123456:
		$ gh release create contrib/envconfig/v1.0.3 --draft --target abc123 --title contrib/envconfig/v1.0.3 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		https://github.com/temporalio/sdk-go/releases/tag/draft

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Creating draft releases for contrib/envconfig=1.0.3

		[1/4] Fetch origin, prepare changelog tool, and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk/contrib/envconfig: passed inspection ✔
		[3/4] Get release notes
		[4/4] Create draft release(s)
		      Draft release: https://github.com/temporalio/sdk-go/releases/tag/draft

		To roll back successful draft releases, run:
		      gh release delete contrib/envconfig/v1.0.3 --yes
	`)
}

func TestReleaseLintBeforePublish(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	input := make(map[string]string)
	input["go.mod"] = "module go.temporal.io/sdk\n"
	input["internal/version.go"] = "const (\n\tSDKVersion = \"1.48.0\"\n)"
	input["CHANGELOG.md"] = "# Changelog\n\n## [1.48.0] - 2026-08-04\n\n### Fixed\n\n- A fix.\n"
	input["contrib/envconfig/go.mod"] = "module go.temporal.io/sdk/contrib/envconfig\n"
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n\n## [1.0.3] - 2026-08-03\n"
	a, eff := newMockRelease(releaseApp{commit: "abc123", modules: []pure.VersionedModule{
		{Module: pure.SDKModule(), Version: pure.Version{1, 48, 0}},
		{Module: contribEnvconfig(t), Version: pure.Version{1, 0, 3}},
	}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md":
			return "", errors.New("wrong release date")
		}
		return "", nil
	})
	err := a.run()
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options abc123^{commit}
		abc123

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 abc123

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.48.0 --date 2026-08-04 --changelog CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 1.0.3 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md
		wrong release date

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Creating draft releases for sdk=1.48.0 contrib/envconfig=1.0.3

		[1/4] Fetch origin, prepare changelog tool, and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk: validate go.mod
		      go.temporal.io/sdk: verify that go.mod requirements are published
		      go.temporal.io/sdk: validate version.go
		      go.temporal.io/sdk: validate changelog
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk: passed inspection ✔
		      go.temporal.io/sdk/contrib/envconfig: failed inspection ✘
	`)
	testEqual(t, fmt.Sprint(err), "go.temporal.io/sdk/contrib/envconfig: changelog-tool release-notes: wrong release date")
	testEqualMaps(t, eff.files, input)
}

func TestReleaseStillReportsCreatedDraftsOnFailure(t *testing.T) {
	t.Setenv("PUBLISHER_SDK_RUST", "/sdk-rust")
	tally, err := pure.ContribModule("contrib/tally")
	if err != nil {
		t.Fatal(err)
	}
	input := make(map[string]string)
	input["contrib/envconfig/go.mod"] = "module go.temporal.io/sdk/contrib/envconfig\n"
	input["contrib/tally/go.mod"] = "module go.temporal.io/sdk/contrib/tally\n"
	input["contrib/envconfig/CHANGELOG.md"] = "# Changelog\n\n## [0.1.0] - 2026-08-04\n\n### Added\n\n- A feature.\n"
	input["contrib/tally/CHANGELOG.md"] = "# Changelog\n\n## [0.1.0] - 2026-08-04\n\n### Added\n\n- A feature.\n"
	notes := "## Notable Changes\n\n### Added\n\n- A feature.\n"
	output := maps.Clone(input)
	output["prepare-release-notes.md"] = notes
	a, eff := newMockRelease(releaseApp{commit: "abc123", modules: []pure.VersionedModule{
		{Module: contribEnvconfig(t), Version: pure.Version{0, 1, 0}},
		{Module: tally, Version: pure.Version{0, 1, 0}},
	}}, input, func(cmd string) (string, error) {
		switch cmd {
		case "git rev-parse --verify --end-of-options abc123^{commit}":
			return "abc123\n", nil
		case "cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 0.1.0 --changelog contrib/envconfig/CHANGELOG.md",
			"cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo " + mockTempDir + " --version 0.1.0 --changelog contrib/tally/CHANGELOG.md":
			return notes, nil
		case "gh release create contrib/envconfig/v0.1.0 --draft --target abc123 --title contrib/envconfig/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "https://github.com/temporalio/sdk-go/releases/tag/untagged-envconfig\n", nil
		case "gh release create contrib/tally/v0.1.0 --draft --target abc123 --title contrib/tally/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false":
			return "", errors.New("command failed")
		}
		return "", nil
	})
	err = a.run()
	testEqualMaps(t, eff.files, output)
	testEqual(t, eff.audit.String(), `
		# Running in /repo:
		$ git fetch --tags origin

		# Running in /sdk-rust:
		$ git rev-parse HEAD
		`+strings.TrimSpace(sdkRustRevision)+`

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- --help --repo /repo

		# Running in /repo:
		$ git rev-parse --verify --end-of-options abc123^{commit}
		abc123

		# Running in /repo:
		$ git worktree add --detach /tmp/prepare-go-release-123456 abc123

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 0.1.0 --date 2026-08-04 --changelog contrib/envconfig/CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 0.1.0 --date 2026-08-04 --changelog contrib/tally/CHANGELOG.md

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 0.1.0 --changelog contrib/envconfig/CHANGELOG.md
		## Notable Changes

		### Added

		- A feature.

		# Running in /sdk-rust:
		$ cargo run --quiet --manifest-path /sdk-rust/crates/changelog-release-notes/Cargo.toml --bin changelog-tool -- release-notes --repo /tmp/prepare-go-release-123456 --version 0.1.0 --changelog contrib/tally/CHANGELOG.md
		## Notable Changes

		### Added

		- A feature.

		# Running in /tmp/prepare-go-release-123456:
		$ gh release create contrib/envconfig/v0.1.0 --draft --target abc123 --title contrib/envconfig/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		https://github.com/temporalio/sdk-go/releases/tag/untagged-envconfig

		# Running in /tmp/prepare-go-release-123456:
		$ gh release create contrib/tally/v0.1.0 --draft --target abc123 --title contrib/tally/v0.1.0 --notes-file /tmp/prepare-go-release-123456/prepare-release-notes.md --latest=false
		command failed

		# Running in /repo:
		$ git worktree remove --force /tmp/prepare-go-release-123456
	`)
	testEqual(t, eff.output.String(), `
		Creating draft releases for contrib/envconfig=0.1.0 contrib/tally=0.1.0

		[1/4] Fetch origin, prepare changelog tool, and create worktree
		[2/4] Lint released modules
		      go.temporal.io/sdk/contrib/envconfig: validate go.mod
		      go.temporal.io/sdk/contrib/envconfig: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/envconfig: validate changelog
		      go.temporal.io/sdk/contrib/tally: validate go.mod
		      go.temporal.io/sdk/contrib/tally: verify that go.mod requirements are published
		      go.temporal.io/sdk/contrib/tally: validate changelog
		      go.temporal.io/sdk/contrib/envconfig: passed inspection ✔
		      go.temporal.io/sdk/contrib/tally: passed inspection ✔
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
		{name: "lint changelog", args: []string{"lint", "abc123"}},
		{name: "skip changelog", args: []string{"lint", "-skip-changelog=true", "abc123"}},
		{name: "missing changelog base", args: []string{"lint"}, wantErr: "usage: publisher lint [-skip-changelog] BASE_COMMIT"},
		{name: "extra changelog argument", args: []string{"lint", "abc123", "extra"}, wantErr: "usage: publisher lint [-skip-changelog] BASE_COMMIT"},
		{name: "lint release commit", args: []string{"lint-release", "abc123", "sdk=1.48.0"}},
		{name: "SDK PR", args: []string{"pr", "sdk=1.48.0"}},
		{name: "contrib PR", args: []string{"pr", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "nested contrib module", args: []string{"pr", "contrib/aws/s3driver/awssdkv2=0.2.2"}, wantDirs: []string{"contrib/aws/s3driver/awssdkv2"}},
		{name: "trailing slash", args: []string{"pr", "contrib/envconfig/=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "SDK release", args: []string{"release", "abc123", "sdk=1.48.0"}},
		{name: "contrib release", args: []string{"release", "abc123", "contrib/envconfig=1.0.3"}, wantDirs: []string{"contrib/envconfig"}},
		{name: "unknown command", args: []string{"unknown"}, wantErr: "unknown command \"unknown\"\nusage: publisher (find|lint|pr|lint-release|release)"},
		{name: "old invocation", args: []string{"1.48.0"}, wantErr: "unknown command \"1.48.0\"\nusage: publisher (find|lint|pr|lint-release|release)"},
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
				testEqual(t, got.baseCommit, "abc123")
				if want := test.name == "skip changelog"; got.skipChangelog != want {
					t.Fatalf("skip-changelog: got %t, want %t", got.skipChangelog, want)
				}
				return
			case *lintReleaseApp:
				name, commit, modules = "lint-release", got.commit, got.modules
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
			if (name == "release" || name == "lint-release") && commit != "abc123" {
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
