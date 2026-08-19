package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// validateVersion accepts release versions with a semantic version core.
func validateVersion(version string) (string, error) {
	if !versionRE.MatchString(version) {
		return "", fmt.Errorf("invalid version %q; expected a version like '1.48.0'", version)
	}
	return version, nil
}

// validateVersionIncrease checks that the next version is a valid increment of the current version.
func validateVersionIncrease(currentVersion, nextVersion string) error {
	currentVersion, err := validateVersion(currentVersion)
	if err != nil {
		return err
	}
	nextVersion, err = validateVersion(nextVersion)
	if err != nil {
		return err
	}

	parse := func(version string) ([3]int, error) {
		var parsed [3]int
		for i, part := range strings.Split(version, ".") {
			parsed[i], err = strconv.Atoi(part)
			if err != nil {
				return [3]int{}, fmt.Errorf("invalid numeric version %q: %w", version, err)
			}
		}
		return parsed, nil
	}
	current, err := parse(currentVersion)
	if err != nil {
		return err
	}
	next, err := parse(nextVersion)
	if err != nil {
		return err
	}

	nextPatch := [3]int{current[0], current[1], current[2] + 1}
	nextMinor := [3]int{current[0], current[1] + 1, 0}
	if next == nextPatch || next == nextMinor {
		return nil
	}
	return fmt.Errorf("version %s must increment the minor or patch version of %s by one", nextVersion, currentVersion)
}

// extractSDKVersion returns the current SDK version, given the contents of internal/version.go.
func extractSDKVersion(versionGo string) (string, error) {
	declarations := sdkVersionRE.FindAllString(versionGo, -1)
	if len(declarations) != 1 {
		return "", errors.New("could not find exactly one SDKVersion declaration in internal/version.go")
	}
	currentVersion := strings.Split(declarations[0], `"`)[1]
	return validateVersion(currentVersion)
}

// validateGoMod requires go.temporal.io/api to use a tagged version (1.XX.YY format)
// instead of a git snapshot (1.XX.YY-0.YYYYMMDDHHMMSS-abcdef123456 format).
func validateGoMod(data string) error {
	match := apiVersionRE.FindStringSubmatch(data)
	if match == nil {
		return errors.New("could not find go.temporal.io/api in go.mod")
	}
	if !taggedGoVersionRE.MatchString(match[1]) {
		return fmt.Errorf("go.temporal.io/api must use an official release, found %q", match[1])
	}
	return nil
}

// validateChangelog requires one Unreleased section, one section for the
// current SDK version, and no duplicate release sections.
func validateChangelog(text, currentVersion string) error {
	if _, err := validateVersion(currentVersion); err != nil {
		return err
	}

	// Count the number of sections for each version.
	counts := make(map[string]int)
	var versions []string
	for _, line := range strings.Split(text, "\n") {
		match := changelogHeadingRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if counts[match[1]] == 0 {
			versions = append(versions, match[1])
		}
		counts[match[1]]++
	}

	// Report missing and duplicate sections
	var problems []string
	for _, version := range []string{"Unreleased", currentVersion} {
		if count := counts[version]; count != 1 {
			problems = append(problems, fmt.Sprintf("expected exactly one section for %q, found %d", version, count))
		}
	}
	for _, version := range versions {
		if version != "Unreleased" && version != currentVersion && counts[version] > 1 {
			problems = append(problems, fmt.Sprintf("found %d sections for %q", counts[version], version))
		}
	}
	if len(problems) > 0 {
		return errors.New("invalid changelog: " + strings.Join(problems, "; "))
	}
	return nil
}

// replaceSDKVersion updates the sole SDKVersion declaration in version.go.
func replaceSDKVersion(text, version string) (string, error) {
	_, err := validateVersion(version)
	if err != nil {
		return "", err
	}
	if len(sdkVersionRE.FindAllStringIndex(text, -1)) != 1 {
		return "", errors.New("could not find exactly one SDKVersion declaration in internal/version.go")
	}
	return sdkVersionRE.ReplaceAllString(text, "${1}"+version+"${2}"), nil
}

// updateChangelog moves Unreleased entries into a dated version section.
func updateChangelog(text, version string, releaseDate time.Time) (string, error) {
	_, err := validateVersion(version)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if _, _, _, ok := findVersionSection(lines, version); ok {
		return "", fmt.Errorf("changelog already has a section for %q", version)
	}
	heading, start, end, ok := findVersionSection(lines, "Unreleased")
	if !ok {
		return "", errors.New("could not find changelog section for 'Unreleased'")
	}
	unreleased := stripEmptyChangelogHeaders(stripOuterBlankLines(lines[start:end]))
	if len(unreleased) == 0 {
		return "", errors.New("changelog section for 'Unreleased' appears to be empty")
	}
	next := append([]string{}, lines[:heading]...)
	next = append(next, seededUnreleasedLines()...)
	next = append(next, "## ["+version+"] - "+releaseDate.Format(time.DateOnly), "")
	next = append(next, unreleased...)
	next = append(next, "")
	next = append(next, lines[end:]...)
	return strings.Join(next, "\n") + "\n", nil
}

// prepareReleaseNotes formats one release's changelog sections for GitHub.
func prepareReleaseNotes(text, version string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	_, start, end, ok := findVersionSection(lines, version)
	if !ok {
		return "", fmt.Errorf("could not find changelog section for %q", version)
	}
	sections := stripOuterBlankLines(lines[start:end])
	if len(sections) == 0 {
		return "", fmt.Errorf("changelog section for %q appears to be empty", version)
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

func seededUnreleasedLines() []string {
	lines := []string{"## [Unreleased]", ""}
	for _, header := range changelogHeaders {
		lines = append(lines, "### "+header, "")
	}
	return lines
}

// stripEmptyChangelogHeaders removes recognized sections that contain no content.
func stripEmptyChangelogHeaders(lines []string) []string {
	var filtered []string
	for i := 0; i < len(lines); {
		match := changelogHeaderRE.FindStringSubmatch(lines[i])
		if match == nil || !contains(changelogHeaders, match[1]) {
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

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func hasNonblank(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// formatCommand renders a command with quoting suitable for logs.
func formatCommand(name string, args ...string) string {
	parts := []string{name}
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			arg = strconv.Quote(arg)
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}
