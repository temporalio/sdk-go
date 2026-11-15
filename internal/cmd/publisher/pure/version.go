package pure

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

// Version is the major.minor.patch core of a release version. This tool deliberately
// does not accept prerelease or build metadata.
type Version [3]int

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// ParseVersion splits a release version into its major, minor, and patch components.
func ParseVersion(s string) (Version, error) {
	if !versionRE.MatchString(s) {
		return Version{}, fmt.Errorf("invalid version %q; expected a version like '1.48.0'", s)
	}
	var parsed Version
	for i, part := range strings.Split(s, ".") {
		number, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("invalid numeric version %q: %w", s, err)
		}
		parsed[i] = number
	}
	return parsed, nil
}

// LatestReleasedVersion returns the highest stable version among the tags for a
// module, or nil if none match. Prereleases and nested module tags are ignored.
func LatestReleasedVersion(tags []string, tagPrefix string) *Version {
	var latest *Version
	for _, tag := range tags {
		s, ok := strings.CutPrefix(tag, tagPrefix+"v")
		if !ok {
			continue
		}
		parsed, err := ParseVersion(s)
		if err != nil {
			continue
		}
		if latest == nil || slices.Compare(parsed[:], latest[:]) > 0 {
			latest = &parsed
		}
	}
	return latest
}

// ExtractSDKVersion returns the version named by the sole SDKVersion declaration
// in version.go.
func ExtractSDKVersion(text string) (Version, error) {
	matches := sdkVersionRE.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		return Version{}, fmt.Errorf("expected exactly one SDKVersion declaration in version.go, found %d", len(matches))
	}
	version, err := ParseVersion(matches[0][2])
	if err != nil {
		return Version{}, fmt.Errorf("SDKVersion declaration in version.go: %w", err)
	}
	return version, nil
}

// ValidateVersionConstant checks that the SDK version declaration matches the
// expected release version.
func ValidateVersionConstant(text string, m Module, expectedVersion Version) error {
	declared, err := ExtractSDKVersion(text)
	if err != nil {
		return err
	}
	if declared != expectedVersion {
		return fmt.Errorf("%s declares version %s, expected %s",
			m.VersionFile, declared, expectedVersion)
	}
	return nil
}

// ComputeNewVersionFile updates the sole SDKVersion declaration in version.go.
func ComputeNewVersionFile(text string, version Version) (string, error) {
	_, err := ExtractSDKVersion(text)
	if err != nil {
		return "", err
	}
	return sdkVersionRE.ReplaceAllString(text, "${1}"+version.String()+"${3}"), nil
}
