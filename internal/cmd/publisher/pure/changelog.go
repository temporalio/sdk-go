package pure

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	// Matches changelog headings such as "## [1.48.0] - 2026-08-04".
	changelogHeadingRE = regexp.MustCompile(`^## \[([^]]+)](?:\s+-\s+(.*?))?\s*$`)
	// Matches changelog section headers such as "### :boom: Breaking Changes".
	changelogHeaderRE = regexp.MustCompile(`^### (.+?)\s*$`)
)

var changelogHeaders = []string{
	"Added",
	"Changed",
	"Deprecated",
	":boom: Breaking Changes",
	"Fixed",
	"Security",
}

// ValidateChangelog requires one Unreleased section, one section for the requested
// release version dated today, and no duplicate release sections.
func ValidateChangelog(text string, version Version, today time.Time) error {
	// Count the number of sections for each version.
	counts := make(map[string]int)
	var versions []string
	var releaseDate string
	for _, line := range strings.Split(text, "\n") {
		match := changelogHeadingRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if counts[match[1]] == 0 {
			versions = append(versions, match[1])
		}
		counts[match[1]]++
		if match[1] == version.String() {
			releaseDate = strings.TrimSpace(match[2])
		}
	}

	required := []string{"Unreleased", version.String()}

	// Report missing and duplicate sections
	var problems []string
	for _, version := range required {
		if count := counts[version]; count != 1 {
			problems = append(problems, fmt.Sprintf("expected exactly one section for %q, found %d", version, count))
		}
	}
	for _, version := range versions {
		if !slices.Contains(required, version) && counts[version] > 1 {
			problems = append(problems, fmt.Sprintf("found %d sections for %q", counts[version], version))
		}
	}
	if counts[version.String()] == 1 && releaseDate != today.Format(time.DateOnly) {
		problems = append(problems, fmt.Sprintf("expected section for %q to have date %q, found %q",
			version.String(), today.Format(time.DateOnly), releaseDate))
	}
	if len(problems) > 0 {
		return errors.New("invalid changelog: " + strings.Join(problems, "; "))
	}
	return nil
}

// ComputeNewChangelog moves Unreleased entries into a dated version section and reseeds
// the Unreleased section. It allows empty entries and does not check for existing versions.
func ComputeNewChangelog(text string, version Version, releaseDate time.Time) (string, error) {
	v := version.String()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	heading, start, end, ok := findVersionSection(lines, "Unreleased")
	if !ok {
		return "", errors.New("could not find changelog section for 'Unreleased'")
	}
	unreleased := stripEmptyLevelThreeHeaders(stripOuterBlankLines(lines[start:end]))
	next := append([]string{}, lines[:heading]...)
	next = append(next, seededUnreleasedLines(changelogHeaders)...)
	next = append(next, "## ["+v+"] - "+releaseDate.Format(time.DateOnly), "")
	next = append(next, unreleased...)
	next = append(next, "")
	next = append(next, lines[end:]...)
	return strings.Join(next, "\n") + "\n", nil
}

// ComputeReleaseNotes formats one release's changelog sections for a GitHub release.
func ComputeReleaseNotes(text string, version Version) (string, error) {
	v := version.String()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	_, start, end, ok := findVersionSection(lines, v)
	if !ok {
		return "", fmt.Errorf("could not find changelog section for %q", v)
	}
	sections := stripOuterBlankLines(lines[start:end])
	if len(sections) == 0 {
		return "", fmt.Errorf("changelog section for %q appears to be empty", v)
	}
	return "## Highlights\n\n" + strings.Join(sections, "\n") + "\n", nil
}

// findVersionSection returns the heading and content bounds for a changelog version.
func findVersionSection(lines []string, version string) (heading, start, end int, ok bool) {
	for i, line := range lines {
		match := changelogHeadingRE.FindStringSubmatch(line)
		if match == nil || match[1] != version {
			continue
		}
		end = len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") {
				end = j
				break
			}
		}
		return i, i + 1, end, true
	}
	return 0, 0, 0, false
}

func seededUnreleasedLines(headers []string) []string {
	lines := []string{"## [Unreleased]", ""}
	for _, header := range headers {
		lines = append(lines, "### "+header, "")
	}
	return lines
}

// stripEmptyLevelThreeHeaders removes level-three sections that contain no content.
func stripEmptyLevelThreeHeaders(lines []string) []string {
	var filtered []string
	for i := 0; i < len(lines); {
		if !changelogHeaderRE.MatchString(lines[i]) {
			filtered = append(filtered, lines[i])
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "### ") {
			j++
		}
		content := lines[i+1 : j]
		if hasNonblank(content) {
			filtered = append(filtered, lines[i])
			filtered = append(filtered, content...)
		}
		i = j
	}
	return stripOuterBlankLines(filtered)
}

func stripOuterBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hasNonblank(lines []string) bool {
	return slices.ContainsFunc(lines, func(line string) bool {
		return strings.TrimSpace(line) != ""
	})
}
