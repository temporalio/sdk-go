package pure

import (
	"strings"
	"testing"
	"time"
)

func TestChangelogRegexps(t *testing.T) {
	t.Run("changelog heading", func(t *testing.T) {
		testRegexp(t, changelogHeadingRE,
			[]string{"## [Unreleased]", "## [1.48.0] - 2026-08-04"},
			[]string{"# [Unreleased]", "## Unreleased", "### [1.48.0]"})
	})
	t.Run("changelog header", func(t *testing.T) {
		testRegexp(t, changelogHeaderRE,
			[]string{"### Added", "### :boom: Breaking Changes"},
			[]string{"## Added", "###", "- Added"})
	})
}

func TestValidateChangelog(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		version string
		wantErr string
	}{
		{
			name: "valid",
			text: stripIndentation(`
				## [Unreleased]

				## [1.47.0] - 2026-08-04
			`),
			version: "1.47.0",
		},
		{
			name: "wrong release date",
			text: stripIndentation(`
				## [Unreleased]

				## [1.47.0] - 2026-08-03
			`),
			version: "1.47.0",
			wantErr: `expected section for "1.47.0" to have date "2026-08-04", found "2026-08-03"`,
		},
		{
			name: "missing release date",
			text: stripIndentation(`
				## [Unreleased]

				## [1.47.0]
			`),
			version: "1.47.0",
			wantErr: `expected section for "1.47.0" to have date "2026-08-04", found ""`,
		},
		{
			name: "missing current",
			text: stripIndentation(`
				## [Unreleased]

				## [1.46.0] - 2026-07-07
			`),
			version: "1.47.0",
			wantErr: `exactly one section for "1.47.0", found 0`,
		},
		{
			name: "duplicate current",
			text: stripIndentation(`
				## [Unreleased]

				## [1.47.0]

				## [1.47.0]
			`),
			version: "1.47.0",
			wantErr: `exactly one section for "1.47.0", found 2`,
		},
		{
			name: "duplicate previous",
			text: stripIndentation(`
				## [Unreleased]

				## [1.47.0]

				## [1.46.0]

				## [1.46.0]
			`),
			version: "1.47.0",
			wantErr: `found 2 sections for "1.46.0"`,
		},
		{
			name: "missing release section",
			text: stripIndentation(`
				## [Unreleased]
			`),
			version: "1.0.2",
			wantErr: `exactly one section for "1.0.2", found 0`,
		},
		{
			name: "missing Unreleased section",
			text: stripIndentation(`
				# Changelog
			`),
			version: "1.0.0",
			wantErr: `exactly one section for "Unreleased", found 0`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateChangelog(test.text, mustVersion(t, test.version), time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC))
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
			}
		})
	}
}

func TestComputeNewChangelog(t *testing.T) {
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
	got, err := ComputeNewChangelog(input, Version{1, 3, 0}, date)
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

func TestComputeNewChangelogUsesCanonicalSeededHeaders(t *testing.T) {
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
	got, err := ComputeNewChangelog(input, Version{0, 2, 1}, date)
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
