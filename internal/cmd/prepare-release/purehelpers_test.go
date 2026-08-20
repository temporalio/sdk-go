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
			// Contrib modules are versioned independently of the SDK's 1.x line.
			matches: []string{"1.48.0", "0.2.1", "2.0.0"},
			rejects: []string{"v1.48.0", "1.48", "1.048.0", "1.48.00", "1.48.0-rc.1", "1.48.0+build.1", "1.48.0 release"},
		},
		{
			name:       "contrib module",
			expression: contribModuleRE,
			matches:    []string{"contrib/envconfig", "contrib/opentelemetry-v2", "contrib/aws/s3driver/awssdkv2"},
			rejects: []string{
				"contrib",
				"contrib/",
				"/contrib/envconfig",
				"contrib//envconfig",
				"contrib/../internal",
				"contrib/-envconfig",
				"contrib/Envconfig",
				"internal",
				"",
			},
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
			name:       "module declaration",
			expression: moduleDeclarationRE,
			matches:    []string{"module go.temporal.io/sdk", "module go.temporal.io/sdk/contrib/envconfig"},
			rejects:    []string{"// module go.temporal.io/sdk", "modules go.temporal.io/sdk", "module"},
		},
		{
			name:       "tagged Go version",
			expression: goVersionRE,
			matches:    []string{"v1.63.4", "v1.64.0", "v0.2.1"},
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

func TestModuleRequirementRE(t *testing.T) {
	expression := moduleRequirementRE("go.temporal.io/sdk")
	goMod := stripIndentation(`
		module go.temporal.io/sdk/contrib/envconfig

		require (
			go.temporal.io/sdk v1.48.0
		)

		require go.temporal.io/api v1.63.4 // indirect

		replace go.temporal.io/sdk => ../../
	`)
	match := expression.FindStringSubmatch(goMod)
	if match == nil || match[1] != "v1.48.0" {
		t.Fatalf("unexpected requirement match: %v", match)
	}

	// Neither the module declaration, the replace directive, nor a longer module path
	// with the same prefix is a requirement on go.temporal.io/sdk.
	for _, goMod := range []string{
		"module go.temporal.io/sdk\n",
		"replace go.temporal.io/sdk => ../../\n",
		"require go.temporal.io/sdk/contrib/envconfig v1.0.2\n",
	} {
		if expression.MatchString(goMod) {
			t.Errorf("expected %q not to match %s", goMod, expression)
		}
	}

	// Indirect requirements still declare a version worth validating.
	indirect := moduleRequirementRE("go.temporal.io/api").FindStringSubmatch(goMod)
	if indirect == nil || indirect[1] != "v1.63.4" {
		t.Fatalf("unexpected indirect requirement match: %v", indirect)
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

		// Contrib modules live on 0.x lines and may graduate to a stable release.
		{name: "prerelease patch", current: "0.2.0", next: "0.2.1", valid: true},
		{name: "prerelease minor", current: "0.2.1", next: "0.3.0", valid: true},
		{name: "graduate to stable", current: "0.2.1", next: "1.0.0", valid: true},
		{name: "graduate past stable", current: "0.2.1", next: "2.0.0"},

		// An unreleased module has no baseline to increment from.
		{name: "first patch release", current: "", next: "0.0.1", valid: true},
		{name: "first minor release", current: "", next: "0.1.0", valid: true},
		{name: "first stable release", current: "", next: "1.0.0", valid: true},
		{name: "first release too high", current: "", next: "0.4.0"},
		{name: "first release matching the SDK", current: "", next: "1.48.0"},
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

func TestLatestReleasedVersion(t *testing.T) {
	tests := []struct {
		name      string
		tags      []string
		tagPrefix string
		want      string
	}{
		{name: "no tags", tagPrefix: "contrib/envconfig/"},
		{
			name:      "highest wins regardless of order",
			tags:      []string{"contrib/envconfig/v1.0.2", "contrib/envconfig/v0.1.0", "contrib/envconfig/v1.0.10"},
			tagPrefix: "contrib/envconfig/",
			want:      "1.0.10",
		},
		{
			name: "SDK tags have no prefix",
			tags: []string{"v1.47.0", "v1.9.0", "v1.48.0"},
			want: "1.48.0",
		},
		{
			name:      "nested modules and prereleases are not releases of this module",
			tags:      []string{"contrib/aws/s3driver/v0.2.1", "contrib/aws/s3driver/awssdkv2/v0.9.0", "contrib/aws/s3driver/v0.3.0-rc.1"},
			tagPrefix: "contrib/aws/s3driver/",
			want:      "0.2.1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := latestReleasedVersion(test.tags, test.tagPrefix)
			if got != test.want {
				t.Fatalf("unexpected latest version: got %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateModulePath(t *testing.T) {
	goMod := "module go.temporal.io/sdk/contrib/envconfig\n\nrequire go.temporal.io/sdk v1.48.0\n"

	err := validateModulePath(goMod, "go.temporal.io/sdk/contrib/envconfig")
	if err != nil {
		t.Fatal(err)
	}

	err = validateModulePath(goMod, "go.temporal.io/sdk/contrib/tally")
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Fatalf("expected module path mismatch error, got %v", err)
	}

	err = validateModulePath("require go.temporal.io/sdk v1.48.0\n", "go.temporal.io/sdk/contrib/envconfig")
	if err == nil || !strings.Contains(err.Error(), "could not find a module declaration") {
		t.Fatalf("expected missing module declaration error, got %v", err)
	}
}

func TestValidateDependency(t *testing.T) {
	tests := []struct {
		name       string
		goMod      string
		modulePath string
		valid      bool
	}{
		{name: "release", goMod: "require go.temporal.io/api v1.63.4\n", modulePath: "go.temporal.io/api", valid: true},
		{name: "prerelease", goMod: "require go.temporal.io/api v1.64.0-rc.1\n", modulePath: "go.temporal.io/api"},
		{
			name:       "pseudo-version",
			goMod:      "require go.temporal.io/api v1.63.1-0.20260730213819-7f6a96199578\n",
			modulePath: "go.temporal.io/api",
		},
		{
			name:       "commit pseudo-version",
			goMod:      "require go.temporal.io/api v0.0.0-20260730213819-7f6a96199578\n",
			modulePath: "go.temporal.io/api",
		},
		{name: "missing", goMod: "module example.com/test\n", modulePath: "go.temporal.io/api"},
		{
			name:       "contrib depends on a released SDK",
			goMod:      "module go.temporal.io/sdk/contrib/tally\n\nrequire go.temporal.io/sdk v1.12.0\n",
			modulePath: "go.temporal.io/sdk",
			valid:      true,
		},
		{
			name:       "contrib depends on an unreleased SDK",
			goMod:      "module go.temporal.io/sdk/contrib/tally\n\nrequire go.temporal.io/sdk v1.48.1-0.20260804123456-abcdef123456\n",
			modulePath: "go.temporal.io/sdk",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDependency(test.goMod, test.modulePath)
			if test.valid && err != nil {
				t.Fatalf("expected dependency to be valid, got %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("expected dependency validation error")
			}
		})
	}
}

func TestValidateChangelog(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		current string
		wantErr string
	}{
		{name: "valid", text: "## [Unreleased]\n\n## [1.47.0] - 2026-07-28\n", current: "1.47.0"},
		{name: "missing current", text: "## [Unreleased]\n\n## [1.46.0] - 2026-07-07\n", current: "1.47.0", wantErr: `exactly one section for "1.47.0", found 0`},
		{name: "duplicate current", text: "## [Unreleased]\n\n## [1.47.0]\n\n## [1.47.0]\n", current: "1.47.0", wantErr: `exactly one section for "1.47.0", found 2`},
		{name: "duplicate previous", text: "## [Unreleased]\n\n## [1.47.0]\n\n## [1.46.0]\n\n## [1.46.0]\n", current: "1.47.0", wantErr: `found 2 sections for "1.46.0"`},

		// An unreleased module has no release sections yet.
		{name: "unreleased module", text: "## [Unreleased]\n"},
		{name: "unreleased module without a heading", text: "# Changelog\n", wantErr: `exactly one section for "Unreleased", found 0`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateChangelog(test.text, test.current)
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

func TestUpdateChangelogUsesCanonicalSeededHeaders(t *testing.T) {
	input := `# Changelog

## [Unreleased]

### Breaking Changes

### Module-Specific Section

### Fixed

- A fix.

## [0.2.0] - 2026-01-01

### Added

- An older feature.
`
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	got, err := updateChangelog(input, "0.2.1", date)
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

## [0.2.1] - 2026-08-04

### Fixed

- A fix.

## [0.2.0] - 2026-01-01

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
