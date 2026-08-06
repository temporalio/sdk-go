package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDryRunWritesUpdatedFileToTempDir(t *testing.T) {
	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "version.go")
	if err := os.WriteFile(inputPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outputDir := t.TempDir()
	var output bytes.Buffer
	eff := DryRun{Output: &output, TempDir: outputDir}
	if err := eff.updateFile(inputPath, func(string) (string, error) {
		return "new\n", nil
	}); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(outputDir, "version.go")
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Fatalf("unexpected dry-run file contents: %q", got)
	}
	if want := "write " + outputPath + "\n"; output.String() != want {
		t.Fatalf("unexpected dry-run output: got %q, want %q", output.String(), want)
	}
}

// HELPER TESTS

func TestRegularExpressions(t *testing.T) {
	tests := []struct {
		name       string
		expression *regexp.Regexp
		matches    []string
		rejects    []string
	}{
		{
			name:       "version",
			expression: versionRE,
			matches:    []string{"1.48.0"},
			rejects:    []string{"v1.48.0", "1.48", "1.48.0-rc.1", "1.48.0+build.1", "1.48.0 release"},
		},
		{
			name:       "changelog heading",
			expression: changelogHeadingRE,
			matches:    []string{"## [Unreleased]", "## [1.48.0] - 2026-08-04"},
			rejects:    []string{"# [Unreleased]", "## Unreleased", "### [1.48.0]"},
		},
		{
			name:       "changelog header",
			expression: changelogHeaderRE,
			matches:    []string{"### Added", "### :boom: Breaking Changes"},
			rejects:    []string{"## Added", "###", "- Added"},
		},
		{
			name:       "SDK version declaration",
			expression: sdkVersionRE,
			matches:    []string{`SDKVersion = "1.47.0"`, "\tSDKVersion = \"1.48.0\""},
			rejects:    []string{`SDKName = "temporal-go"`, `SDKVersion := "1.48.0"`},
		},
		{
			name:       "API dependency",
			expression: apiVersionRE,
			matches:    []string{"go.temporal.io/api v1.63.4", "require go.temporal.io/api v1.63.4"},
			rejects:    []string{"go.temporal.io/sdk v1.63.4", "go.temporal.io/api"},
		},
		{
			name:       "tagged Go version",
			expression: taggedGoVersionRE,
			matches:    []string{"v1.63.4", "v1.64.0"},
			rejects: []string{
				"1.63.4",
				"v1.63",
				"v1.64.0-rc.1",
				"v1.64.0+build.1",
				"v0.0.0-20260730213819-7f6a96199578",
				"v1.63.1-0.20260730213819-7f6a96199578",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, input := range test.matches {
				if !test.expression.MatchString(input) {
					t.Errorf("expected %q to match %s", input, test.expression)
				}
			}
			for _, input := range test.rejects {
				if test.expression.MatchString(input) {
					t.Errorf("expected %q not to match %s", input, test.expression)
				}
			}
		})
	}
}

func TestUpdateChangelog(t *testing.T) {
	input := `# Changelog

## [Unreleased]

### Added

- A feature.

### Changed

### Fixed

- A fix.

## [1.2.0] - 2026-01-01

### Added

- An older feature.
`
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	got, err := updateChangelog(input, "1.3.0", date)
	if err != nil {
		t.Fatal(err)
	}
	want := `# Changelog

## [Unreleased]

### Added

### Changed

### Deprecated

### :boom: Breaking Changes

### Fixed

### Security

## [1.3.0] - 2026-08-04

### Added

- A feature.

### Fixed

- A fix.

## [1.2.0] - 2026-01-01

### Added

- An older feature.
`
	if got != want {
		t.Fatalf("unexpected changelog:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestUpdateChangelogRejectsInvalidState(t *testing.T) {
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)

	_, err := updateChangelog("# Changelog\n", "1.3.0", date)
	if err == nil || !strings.Contains(err.Error(), "could not find") {
		t.Fatalf("expected missing Unreleased error, got %v", err)
	}

	emptyUnreleased := `
## [Unreleased]

### Added
`
	_, err = updateChangelog(emptyUnreleased, "1.3.0", date)
	if err == nil || !strings.Contains(err.Error(), "appears to be empty") {
		t.Fatalf("expected empty Unreleased error, got %v", err)
	}

	duplicateRelease := `
## [Unreleased]

- New

## [1.3.0] - 2026-01-01
`
	_, err = updateChangelog(duplicateRelease, "1.3.0", date)
	if err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("expected duplicate release error, got %v", err)
	}
}

func TestReplaceSDKVersion(t *testing.T) {
	input := `
const (
	SDKVersion = "1.47.0"
	SupportedServerVersions = ">=1.0.0 <2.0.0"
)
`
	got, err := replaceSDKVersion(input, "1.48.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `SDKVersion = "1.48.0"`) {
		t.Fatalf("SDKVersion was not replaced:\n%s", got)
	}
	if !strings.Contains(got, `SupportedServerVersions = ">=1.0.0 <2.0.0"`) {
		t.Fatalf("SupportedServerVersions changed:\n%s", got)
	}
}

func TestVerifyAPIVersionFile(t *testing.T) {
	release := `
module example.com/test
require go.temporal.io/api v1.63.4
`
	if err := validateGoMod(release); err != nil {
		t.Fatalf("expected release version to be valid, got %v", err)
	}

	prerelease := `
module example.com/test
require go.temporal.io/api v1.64.0-rc.1
`
	if err := validateGoMod(prerelease); err == nil {
		t.Fatal("expected prerelease version to be invalid")
	}

	pseudoVersion := `
module example.com/test
require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578
`
	if err := validateGoMod(pseudoVersion); err == nil {
		t.Fatal("expected pseudo-version to be invalid")
	}

	commitPseudoVersion := `
module example.com/test
require go.temporal.io/api v0.0.0-20260730213819-7f6a96199578
`
	if err := validateGoMod(commitPseudoVersion); err == nil {
		t.Fatal("expected commit pseudo-version to be invalid")
	}
}

// INTEGRATION TESTS WITH MOCK WORLD

func TestPrepareReleaseWithMockWorld(t *testing.T) {
	root := "/repo"
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	version := "1.48.0"

	// INPUT

	changelog := `
# Changelog

## [Unreleased]

### Fixed

- A fix.
`
	goMod := `
module go.temporal.io/sdk
require go.temporal.io/api v1.63.4
`
	versionGo := `
const (
	SDKVersion = "1.47.0"
)
`

	// EXPECTED OUTPUT

	updatedChangelog := `# Changelog

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
`
	updatedVersionGo := `
const (
	SDKVersion = "1.48.0"
)
`
	wantCommands := []string{
		"git status --porcelain",
		"git fetch origin main",
		"git switch --create chore/release-1.48.0 origin/main",
		`git commit -m "Prepare release 1.48.0" -- CHANGELOG.md internal/version.go`,
		"git push --set-upstream origin chore/release-1.48.0",
		`gh pr create --base main --head chore/release-1.48.0 --title "Prepare release 1.48.0" --body "Prepare Go SDK release 1.48.0."`,
	}

	// TESTS

	eff := &MockWorld{
		Changelog: changelog,
		Files: map[string]string{
			filepath.Join(root, "go.mod"):                 goMod,
			filepath.Join(root, "internal", "version.go"): versionGo,
		},
	}
	if err := prepareRelease(eff, root, version, date); err != nil {
		t.Fatal(err)
	}

	if eff.Changelog != updatedChangelog {
		t.Fatalf("unexpected changelog:\n--- got ---\n%s--- want ---\n%s", eff.Changelog, updatedChangelog)
	}
	if got := eff.Files[filepath.Join(root, "go.mod")]; got != goMod {
		t.Fatalf("unexpected go.mod:\n--- got ---\n%s--- want ---\n%s", got, goMod)
	}
	if got := eff.Files[filepath.Join(root, "internal", "version.go")]; got != updatedVersionGo {
		t.Fatalf("unexpected version.go:\n--- got ---\n%s--- want ---\n%s", got, updatedVersionGo)
	}
	if strings.Join(eff.Commands, "\n") != strings.Join(wantCommands, "\n") {
		t.Fatalf("unexpected commands:\n--- got ---\n%s\n--- want ---\n%s", strings.Join(eff.Commands, "\n"), strings.Join(wantCommands, "\n"))
	}
}
