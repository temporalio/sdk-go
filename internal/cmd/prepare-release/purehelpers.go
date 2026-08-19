package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// validateVersion accepts release versions with a semantic version core.
func validateVersion(version string) (string, error) {
	_, err := parseVersion(version)
	if err != nil {
		return "", err
	}
	return version, nil
}

// parseVersion splits a release version into its major, minor, and patch components.
func parseVersion(version string) ([3]int, error) {
	if !versionRE.MatchString(version) {
		return [3]int{}, fmt.Errorf("invalid version %q; expected a version like '1.48.0'", version)
	}
	var parsed [3]int
	for i, part := range strings.Split(version, ".") {
		number, err := strconv.Atoi(part)
		if err != nil {
			return [3]int{}, fmt.Errorf("invalid numeric version %q: %w", version, err)
		}
		parsed[i] = number
	}
	return parsed, nil
}

func formatVersion(version [3]int) string {
	return fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
}

// validateVersionIncrease checks that the next version is a valid increment of the
// current version. An empty current version means the module has never been released,
// which is treated as a 0.0.0 baseline.
func validateVersionIncrease(currentVersion, nextVersion string) error {
	firstRelease := currentVersion == ""
	if firstRelease {
		currentVersion = "0.0.0"
	}
	current, err := parseVersion(currentVersion)
	if err != nil {
		return err
	}
	next, err := parseVersion(nextVersion)
	if err != nil {
		return err
	}

	allowed := [][3]int{
		{current[0], current[1], current[2] + 1},
		{current[0], current[1] + 1, 0},
	}
	// A module still on a 0.x version may graduate to its first stable release.
	if current[0] == 0 {
		allowed = append(allowed, [3]int{1, 0, 0})
	}
	options := make([]string, len(allowed))
	for i, candidate := range allowed {
		if next == candidate {
			return nil
		}
		options[i] = formatVersion(candidate)
	}

	if firstRelease {
		return fmt.Errorf("version %s cannot be a first release; expected one of: %s",
			nextVersion, strings.Join(options, ", "))
	}
	return fmt.Errorf("version %s does not follow the current version %s; expected one of: %s",
		nextVersion, currentVersion, strings.Join(options, ", "))
}

// latestReleasedVersion returns the highest release version among the tags carrying the
// given prefix, or "" if none of them name a release.
func latestReleasedVersion(tags []string, tagPrefix string) string {
	var latest [3]int
	var latestVersion string
	for _, tag := range tags {
		version, ok := strings.CutPrefix(tag, tagPrefix+"v")
		if !ok {
			continue
		}
		// Tags for nested modules and for prereleases share the prefix but are not
		// releases of this module.
		parsed, err := parseVersion(version)
		if err != nil {
			continue
		}
		if latestVersion == "" || compareVersions(parsed, latest) > 0 {
			latest, latestVersion = parsed, version
		}
	}
	return latestVersion
}

func compareVersions(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
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

// validateModulePath requires go.mod to declare the module the release tag will version.
// A mismatch means the module directory is not the one the caller named.
func validateModulePath(goMod, modulePath string) error {
	match := moduleDeclarationRE.FindStringSubmatch(goMod)
	if match == nil {
		return errors.New("could not find a module declaration in go.mod")
	}
	if match[1] != modulePath {
		return fmt.Errorf("go.mod declares module %q, expected %q", match[1], modulePath)
	}
	return nil
}

// validateDependency requires the given Temporal module to use a tagged version
// (v1.XX.YY format) instead of a git snapshot (v1.XX.YY-0.YYYYMMDDHHMMSS-abcdef123456 format).
func validateDependency(goMod, modulePath string) error {
	match := moduleRequirementRE(modulePath).FindStringSubmatch(goMod)
	if match == nil {
		return fmt.Errorf("could not find %s in go.mod", modulePath)
	}
	if !taggedGoVersionRE.MatchString(match[1]) {
		return fmt.Errorf("%s must use an official release, found %q", modulePath, match[1])
	}
	return nil
}

// moduleRequirementRE matches the go.mod requirement line for the given module path.
func moduleRequirementRE(modulePath string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*(?:require\s+)?` + regexp.QuoteMeta(modulePath) + `\s+(v\S+)\s*(?://.*)?$`)
}

// validateChangelog requires one Unreleased section, one section for the module's
// current version, and no duplicate release sections. An empty current version means
// the module has never been released, so it has no release sections yet.
func validateChangelog(text, currentVersion string) error {
	required := []string{"Unreleased"}
	if currentVersion != "" {
		if _, err := validateVersion(currentVersion); err != nil {
			return err
		}
		required = append(required, currentVersion)
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
	for _, version := range required {
		if count := counts[version]; count != 1 {
			problems = append(problems, fmt.Sprintf("expected exactly one section for %q, found %d", version, count))
		}
	}
	for _, version := range versions {
		if !contains(required, version) && counts[version] > 1 {
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

// updateChangelog moves Unreleased entries into a dated version section, reseeding the
// Unreleased section with the given section headers.
func updateChangelog(text, version string, releaseDate time.Time, seededHeaders []string) (string, error) {
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
	next = append(next, seededUnreleasedLines(seededHeaders)...)
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
	return "# Highlights\n\n" + strings.Join(sections, "\n") + "\n", nil
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

// stripEmptyChangelogHeaders removes recognized sections that contain no content.
func stripEmptyChangelogHeaders(lines []string) []string {
	var filtered []string
	for i := 0; i < len(lines); {
		match := changelogHeaderRE.FindStringSubmatch(lines[i])
		if match == nil || !contains(knownChangelogHeaders, match[1]) {
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
