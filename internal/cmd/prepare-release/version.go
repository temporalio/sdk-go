package main

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	// Matches release versions such as "1.48.0" or "0.2.1".
	versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// Matches the SDK version declaration: SDKVersion = "1.48.0".
	sdkVersionRE = regexp.MustCompile(`(?m)^(\s*SDKVersion\s*=\s*")([^"]+)("\s*)$`)
)

// semver is the major.minor.patch core of a release version. This tool deliberately
// does not accept prerelease or build metadata. A semver value is always a version
// that parseVersion accepted, so consumers do not revalidate it.
type semver [3]int

func (v semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// parseVersion splits a release version into its major, minor, and patch components.
func parseVersion(s string) (semver, error) {
	if !versionRE.MatchString(s) {
		return semver{}, fmt.Errorf("invalid version %q; expected a version like '1.48.0'", s)
	}
	var parsed semver
	for i, part := range strings.Split(s, ".") {
		number, err := strconv.Atoi(part)
		if err != nil {
			return semver{}, fmt.Errorf("invalid numeric version %q: %w", s, err)
		}
		parsed[i] = number
	}
	return parsed, nil
}

// validateVersionIncrease checks that next is a valid increment of the current version.
// The only valid increments are:
//   - patch: 1.48.0 → 1.48.1
//   - minor: 1.48.0 → 1.49.0
//   - major: 0.x.y → 1.0.0
//
// We don't allow a 1.x.y release to bump to 2.0.
func validateVersionIncrease(current, next semver) error {
	allowed := []semver{
		{current[0], current[1], current[2] + 1},
		{current[0], current[1] + 1, 0},
	}
	// A module still on a 0.x version may graduate to its first stable release.
	if current[0] == 0 {
		allowed = append(allowed, semver{1, 0, 0})
	}
	expected := make([]string, len(allowed))
	for i, candidate := range allowed {
		if next == candidate {
			return nil
		}
		expected[i] = candidate.String()
	}

	return fmt.Errorf("version %s does not follow the current version %s; expected one of: %s",
		next, current, strings.Join(expected, ", "))
}

// latestReleasedVersion returns the highest release version among the tags carrying the
// given prefix. It returns nil if none of them name a release of this module.
func latestReleasedVersion(tags []string, tagPrefix string) *semver {
	var latest *semver
	for _, tag := range tags {
		s, ok := strings.CutPrefix(tag, tagPrefix+"v")
		if !ok {
			continue
		}
		// Tags for nested modules and for prereleases share the prefix but are not
		// releases of this module.
		parsed, err := parseVersion(s)
		if err != nil {
			continue
		}
		if latest == nil || slices.Compare(parsed[:], latest[:]) > 0 {
			latest = &parsed
		}
	}
	return latest
}

// extractSDKVersion returns the version named by the sole SDKVersion declaration
// in version.go.
func extractSDKVersion(text string) (semver, error) {
	matches := sdkVersionRE.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return semver{}, fmt.Errorf("expected exactly one SDKVersion declaration in version.go, found %d", len(matches))
	}
	version, err := parseVersion(matches[0][2])
	if err != nil {
		return semver{}, fmt.Errorf("SDKVersion declaration in version.go: %w", err)
	}
	return version, nil
}

// computeNewVersionFile updates the sole SDKVersion declaration in version.go.
func computeNewVersionFile(text string, version semver) (string, error) {
	_, err := extractSDKVersion(text)
	if err != nil {
		return "", err
	}
	return sdkVersionRE.ReplaceAllString(text, "${1}"+version.String()+"${3}"), nil
}
