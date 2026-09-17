package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A misspelled module directory has no go.mod, which is what stops the tool from
// tagging and releasing a module that does not exist.
func TestValidateReleaseRejectsUnknownContribModule(t *testing.T) {
	target, err := contribTarget("contrib/envconfg")
	if err != nil {
		t.Fatal(err)
	}
	a, eff := newMockApp(app{target: target, version: semver{1, 0, 3}}, nil)

	err = a.worktreeAt(eff.tempDir, "").validateEverything()
	missing := filepath.Join(eff.tempDir, "contrib", "envconfg", "go.mod")
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected an error naming %s, got %v", missing, err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a missing file error, got %v", err)
	}
	if strings.Contains(eff.commands.String(), "git tag") {
		t.Fatalf("unknown module was inspected further:\n%s", eff.commands.String())
	}
}

// A directory holding some other module would otherwise be released under the wrong tag.
func TestValidateReleaseRejectsMismatchedModulePath(t *testing.T) {
	target := contribEnvconfig(t)
	a, eff := newMockApp(app{target: target, version: semver{1, 0, 3}}, nil)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] =
		"module go.temporal.io/sdk/contrib/tally\nrequire go.temporal.io/sdk v1.48.0\n"

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), `declares module "go.temporal.io/sdk/contrib/tally", expected "go.temporal.io/sdk/contrib/envconfig"`) {
		t.Fatalf("expected module path mismatch error, got %v", err)
	}
}

// A contrib module cannot be released against an unreleased SDK.
func TestValidateReleaseRejectsUnreleasedSDKDependency(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 0, 3}}, nil)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.1-0.20260804123456-abcdef123456
	`)

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "go.temporal.io/sdk must use an official release") {
		t.Fatalf("expected dependency validation error, got %v", err)
	}
}

func TestValidateReleaseRejectsUnpublishedSiblingRequirement(t *testing.T) {
	target, err := contribTarget("contrib/aws/s3driver/awssdkv2")
	if err != nil {
		t.Fatal(err)
	}
	a, eff := newMockApp(app{target: target, version: semver{0, 2, 2}}, nil)
	eff.moduleLookupHandler = func(modulePath, version string) (bool, error) {
		return version != "v0.0.0", nil
	}
	eff.files[filepath.Join(eff.tempDir, "contrib", "aws", "s3driver", "awssdkv2", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/aws/s3driver/awssdkv2

		require (
			go.temporal.io/sdk v1.43.1
			go.temporal.io/sdk/contrib/aws/s3driver v0.0.0
		)

		replace go.temporal.io/sdk/contrib/aws/s3driver => ../
	`)

	err = a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "go.temporal.io/sdk/contrib/aws/s3driver v0.0.0 is not published") {
		t.Fatalf("expected unpublished sibling error, got %v", err)
	}
	if strings.Contains(eff.commands.String(), "git tag") {
		t.Fatalf("validation continued past the bad go.mod:\n%s", eff.commands.String())
	}
}

func TestValidateReleasePropagatesProxyFailure(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 0, 3}}, nil)
	eff.moduleLookupHandler = func(string, string) (bool, error) {
		return false, errors.New("proxy unreachable")
	}
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "proxy unreachable") {
		t.Fatalf("expected the proxy error to be propagated, got %v", err)
	}
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

	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 47, 0}}, func(cmd string) (string, error) {
		if cmd == "git tag --list v*" {
			return "v1.47.0\n", nil
		}
		return "", nil
	})

	eff.files[filepath.Join(eff.tempDir, "go.mod")] = goMod
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = versionGo

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "does not follow the current version 1.47.0") {
		t.Fatalf("expected version increase error, got %v", err)
	}
	testEqual(t, eff.output.String(), `
		      $ git tag --list v*
	`)
}

// The SDK's version constant is a second, independent record of the current
// version. If it disagrees with the tags, one of them is wrong.
func TestValidateReleaseRejectsStaleVersionConstant(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}}, func(cmd string) (string, error) {
		if cmd == "git tag --list v*" {
			return "v1.46.0\nv1.47.0\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.45.0"`

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "internal/version.go declares version 1.45.0, but the latest release tag is v1.47.0") {
		t.Fatalf("expected version constant mismatch error, got %v", err)
	}
}

// Without tags there is nothing to check the new version against, so an SDK
// release must not proceed as if the module had never been released.
func TestValidateReleaseRejectsMissingSDKTags(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}}, nil)
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.47.0"`

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), `internal/version.go declares version 1.47.0, but no release tags match "v*"`) {
		t.Fatalf("expected missing tag error, got %v", err)
	}
}

func TestValidateReleaseRejectsAlreadyReleasedVersion(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 0, 3}}, func(cmd string) (string, error) {
		if cmd == "git tag --list contrib/envconfig/v*" {
			return "contrib/envconfig/v1.0.2\ncontrib/envconfig/v1.0.3\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), "does not follow the current version 1.0.3") {
		t.Fatalf("expected already-released error, got %v", err)
	}
}

// Contrib modules carry no version constant, so their current version comes from tags.
func TestValidateReleaseUsesTagsForContribVersion(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 0, 3}}, func(cmd string) (string, error) {
		if cmd == "git tag --list contrib/envconfig/v*" {
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

	w := a.worktreeAt(eff.tempDir, "")
	err := w.validateEverything()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      $ git tag --list contrib/envconfig/v*
	`)

	w.version = semver{1, 2, 0}
	err = w.validateEverything()
	if err == nil || !strings.Contains(err.Error(), "does not follow the current version 1.0.2") {
		t.Fatalf("expected version increase error, got %v", err)
	}
}

// An untagged contrib module has no previous release section to validate against.
func TestValidateReleaseAllowsFirstContribRelease(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{0, 1, 0}}, nil)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = "## [Unreleased]\n"

	w := a.worktreeAt(eff.tempDir, "")
	err := w.validateEverything()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      $ git tag --list contrib/envconfig/v*
	`)

	w.version = semver{0, 4, 0}
	err = w.validateEverything()
	if err != nil {
		t.Fatalf("expected a release without a prior version to be valid, got %v", err)
	}
}

// Several contrib changelogs were seeded long after the module's first release, so
// they record no history at all. That must not block the next release, which is the
// very thing that starts the history.
func TestValidateReleaseAllowsContribChangelogWithoutHistory(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 0, 3}}, func(cmd string) (string, error) {
		if cmd == "git tag --list contrib/envconfig/v*" {
			return "contrib/envconfig/v1.0.0\ncontrib/envconfig/v1.0.1\ncontrib/envconfig/v1.0.2\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "go.mod")] = stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig
		require go.temporal.io/sdk v1.48.0
	`)
	eff.files[filepath.Join(eff.tempDir, "contrib", "envconfig", "CHANGELOG.md")] = stripIndentation(`
		# Changelog

		## [Unreleased]

		### Breaking Changes

		- Raised the minimum supported Go version.
	`)

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateReleaseRejectsInvalidChangelog(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 48, 0}}, func(cmd string) (string, error) {
		if cmd == "git tag --list v*" {
			return "v1.47.0\n", nil
		}
		return "", nil
	})
	eff.files[filepath.Join(eff.tempDir, "go.mod")] = "module go.temporal.io/sdk\nrequire go.temporal.io/api v1.63.4\n"
	eff.files[filepath.Join(eff.tempDir, "internal", "version.go")] = `SDKVersion = "1.47.0"`
	eff.files[filepath.Join(eff.tempDir, "CHANGELOG.md")] = "## [Unreleased]\n\n## [1.47.0]\n\n## [1.46.0]\n\n## [1.46.0]\n"

	err := a.worktreeAt(eff.tempDir, "").validateEverything()
	if err == nil || !strings.Contains(err.Error(), `found 2 sections for "1.46.0"`) {
		t.Fatalf("expected duplicate changelog section error, got %v", err)
	}
}

func TestListTags(t *testing.T) {
	a, eff := newMockApp(app{}, func(cmd string) (string, error) {
		if cmd == "git tag --list contrib/envconfig/v*" {
			return "contrib/envconfig/v1.0.0\n\ncontrib/envconfig/v1.0.1\n", nil
		}
		return "", nil
	})

	tags, err := a.worktreeAt("/repo", "").listTags("contrib/envconfig/v*")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tags, ",") != "contrib/envconfig/v1.0.0,contrib/envconfig/v1.0.1" {
		t.Fatalf("unexpected tags: %q", tags)
	}
	testEqual(t, eff.commands.String(), `/repo: git tag --list contrib/envconfig/v*`)
}

func TestCommitRelease(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 2, 3}}, nil)

	err := a.worktreeAt("/worktree", "").commitRelease()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare release 1.2.3" -- CHANGELOG.md internal/version.go
	`)
}

// Contrib releases only update their CHANGELOG.md, not internal/version.go
func TestCommitReleaseForContribModule(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 2, 3}}, nil)

	err := a.worktreeAt("/worktree", "").commitRelease()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git commit -m "Prepare contrib/envconfig release 1.2.3" -- contrib/envconfig/CHANGELOG.md
	`)
}

func TestPushBranch(t *testing.T) {
	a, eff := newMockApp(app{}, nil)

	err := a.worktreeAt("/worktree", "release").pushBranch()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/worktree: git push --set-upstream origin release
	`)
}

func TestOpenDraftPR(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 2, 3}}, func(cmd string) (string, error) {
		if cmd == `gh pr create --draft --base main --head release --label skip-changelog --title "Prepare release 1.2.3" --body "Prepare release 1.2.3"` {
			return "https://example.com/pr\n", nil
		}
		return "", nil
	})

	_, err := a.worktreeAt("/worktree", "release").openDraftPR()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      $ gh pr create --draft --base main --head release --label skip-changelog --title "Prepare release 1.2.3" --body "Prepare release 1.2.3"
		      Draft PR: https://example.com/pr
	`)
}

func TestOpenDraftPRForContribModule(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 2, 3}}, func(cmd string) (string, error) {
		if cmd == `gh pr create --draft --base main --head release --label skip-changelog --title "Prepare contrib/envconfig release 1.2.3" --body "Prepare contrib/envconfig release 1.2.3"` {
			return "https://example.com/pr\n", nil
		}
		return "", nil
	})

	_, err := a.worktreeAt("/worktree", "release").openDraftPR()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      $ gh pr create --draft --base main --head release --label skip-changelog --title "Prepare contrib/envconfig release 1.2.3" --body "Prepare contrib/envconfig release 1.2.3"
		      Draft PR: https://example.com/pr
	`)
}

func TestCreateDraftRelease(t *testing.T) {
	a, eff := newMockApp(app{target: sdkTarget(), version: semver{1, 2, 3}}, func(cmd string) (string, error) {
		if cmd == "gh release create v1.2.3 --draft --title v1.2.3 --notes-file /worktree/prepare-release-notes.md --generate-notes" {
			return "https://example.com/release\n", nil
		}
		return "", nil
	})

	_, err := a.worktreeAt("/worktree", "").createDraftRelease("Notes")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      Release notes: /worktree/prepare-release-notes.md
		      $ gh release create v1.2.3 --draft --title v1.2.3 --notes-file /worktree/prepare-release-notes.md --generate-notes
		      Draft release: https://example.com/release
	`)
	testEqual(t, eff.files[filepath.Join("/worktree", "prepare-release-notes.md")], "Notes")
}

// Contrib releases differ from main releases in three ways:
//  1. Different tags
//  2. They're not marked as the "latest" release
//  3. Their CHANGELOG.md is the whole story, so GitHub does not append a
//     repo-wide commit list to it
func TestCreateDraftReleaseForContribModule(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 2, 3}}, func(cmd string) (string, error) {
		if cmd == "gh release create contrib/envconfig/v1.2.3 --draft --title contrib/envconfig/v1.2.3 --notes-file /worktree/prepare-release-notes.md --latest=false" {
			return "https://example.com/release\n", nil
		}
		return "", nil
	})

	_, err := a.worktreeAt("/worktree", "").createDraftRelease("Notes")
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.output.String(), `
		      Release notes: /worktree/prepare-release-notes.md
		      $ gh release create contrib/envconfig/v1.2.3 --draft --title contrib/envconfig/v1.2.3 --notes-file /worktree/prepare-release-notes.md --latest=false
		      Draft release: https://example.com/release
	`)
	testEqual(t, eff.files[filepath.Join("/worktree", "prepare-release-notes.md")], "Notes")
}

func TestUpdateFile(t *testing.T) {
	a, eff := newMockApp(app{}, nil)
	eff.files["version.txt"] = "old"

	updated, err := a.worktreeAt("/worktree", "").updateFile("version.txt", func(contents string) (string, error) {
		return contents + "-new", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated != "old-new" || eff.files["version.txt"] != "old-new" {
		t.Fatalf("unexpected updated file: returned %q, stored %q", updated, eff.files["version.txt"])
	}
}
