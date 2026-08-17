package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

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
			rejects:    []string{"2.0.0", "v1.48.0", "1.48", "1.048.0", "1.48.00", "1.48.0-rc.1", "1.48.0+build.1", "1.48.0 release"},
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
				"v1.063.4",
				"v1.63.04",
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

func TestValidateVersionIncrease(t *testing.T) {
	tests := []struct {
		name    string
		current string
		next    string
		valid   bool
	}{
		{name: "patch", current: "1.47.0", next: "1.47.1", valid: true},
		{name: "minor", current: "1.47.9", next: "1.48.0", valid: true},
		{name: "equal", current: "1.47.0", next: "1.47.0"},
		{name: "skip patch", current: "1.47.0", next: "1.47.2"},
		{name: "skip minor", current: "1.47.0", next: "1.49.0"},
		{name: "minor without patch reset", current: "1.47.9", next: "1.48.1"},
		{name: "major", current: "1.47.9", next: "2.0.0"},
		{name: "lower", current: "1.48.0", next: "1.47.9"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateVersionIncrease(test.current, test.next)
			if test.valid && err != nil {
				t.Fatalf("expected version increase to be valid, got %v", err)
			}
			if !test.valid && err == nil {
				t.Fatalf("expected version increase error, got %v", err)
			}
		})
	}
}

func TestValidateGoMod(t *testing.T) {
	release := `
module example.com/test
require go.temporal.io/api v1.63.4
`
	err := validateGoMod(release)
	if err != nil {
		t.Fatalf("expected release version to be valid, got %v", err)
	}

	prerelease := `
module example.com/test
require go.temporal.io/api v1.64.0-rc.1
`
	err = validateGoMod(prerelease)
	if err == nil {
		t.Fatal("expected prerelease version to be invalid")
	}

	pseudoVersion := `
module example.com/test
require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578
`
	err = validateGoMod(pseudoVersion)
	if err == nil {
		t.Fatal("expected pseudo-version to be invalid")
	}

	commitPseudoVersion := `
module example.com/test
require go.temporal.io/api v0.0.0-20260730213819-7f6a96199578
`
	err = validateGoMod(commitPseudoVersion)
	if err == nil {
		t.Fatal("expected commit pseudo-version to be invalid")
	}
}

func TestValidateChangelog(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{name: "valid", text: "## [Unreleased]\n\n## [1.47.0] - 2026-07-28\n"},
		{name: "missing current", text: "## [Unreleased]\n\n## [1.46.0] - 2026-07-07\n", wantErr: `exactly one section for "1.47.0", found 0`},
		{name: "duplicate current", text: "## [Unreleased]\n\n## [1.47.0]\n\n## [1.47.0]\n", wantErr: `exactly one section for "1.47.0", found 2`},
		{name: "duplicate previous", text: "## [Unreleased]\n\n## [1.47.0]\n\n## [1.46.0]\n\n## [1.46.0]\n", wantErr: `found 2 sections for "1.46.0"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateChangelog(test.text, "1.47.0")
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
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

func TestUpdateChangelogPreservesHistory(t *testing.T) {
	history := "## [1.2.0] - 2026-01-01\n\n```go\nfirst()\n\n\nsecond()\n```\n"
	input := "## [Unreleased]\n\n### Fixed\n\n- A fix.\n\n" + history
	got, err := updateChangelog(input, "1.3.0", time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, history) {
		t.Fatalf("historical changelog changed:\n%s", got)
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
