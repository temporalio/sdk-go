package main

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const sdkModulePath = "go.temporal.io/sdk"

// Matches relative paths of contrib module directories like "contrib/envconfig"
// and "contrib/aws/s3driver/awssdkv2".
var contribModuleRE = regexp.MustCompile(`^contrib(/[a-z0-9][a-z0-9._-]*)+$`)

// releaseTarget describes a Go module in this repository.
type releaseTarget struct {
	// dir is the module directory relative to the repository root in slash form,
	// e.g. "contrib/envconfig". Empty for the main SDK module.
	dir string
	// versionFile is "internal/version.go" if we're releasing the main SDK module.
	// Otherwise it's an empty string.
	versionFile string
	// markLatest is whether GitHub should mark the release as the repository's
	// latest release.
	markLatest bool
	// generateNotes is whether GitHub should populate draft releases with
	// --generate-notes.
	generateNotes bool
}

// sdkTarget describes the main go.temporal.io/sdk module
func sdkTarget() releaseTarget {
	return releaseTarget{
		versionFile:   "internal/version.go",
		markLatest:    true,
		generateNotes: true,
	}
}

// contribTarget describes a contrib module, given its directory relative to the
// repository root, like "contrib/envconfig" or "contrib/aws/s3driver/awssdkv2".
// Requires UNIX slash syntax.
func contribTarget(dir string) (releaseTarget, error) {
	dir = strings.TrimSuffix(strings.TrimPrefix(dir, "./"), "/")
	if !contribModuleRE.MatchString(dir) {
		return releaseTarget{}, fmt.Errorf(
			"invalid module %q; expected a slash-separated contrib module directory such as 'contrib/envconfig'", dir)
	}
	return releaseTarget{
		dir:           dir,
		markLatest:    false,
		generateNotes: false,
	}, nil
}

// modulePath the target's module path, e.g. "go.temporal.io/sdk/contrib/envconfig".
func (t releaseTarget) modulePath() string {
	return path.Join(sdkModulePath, t.dir)
}

// tagPrefix returns what precedes "v<version>" in the module's Go release tag.
// For the main SDK module, it's empty.
// For a contrib module like "contrib/envconfig", it's "contrib/envconfig/".
func (t releaseTarget) tagPrefix() string {
	if t.dir == "" {
		return ""
	}
	return t.dir + "/"
}

// tag returns the Go module release tag for the given version.
func (t releaseTarget) tag(version semver) string {
	return t.tagPrefix() + "v" + version.String()
}

// tagPattern is a glob pattern that matches release tags belonging to this module.
func (t releaseTarget) tagPattern() string {
	return t.tagPrefix() + "v*"
}

// changelog returns the module's changelog relative to the repository root.
func (t releaseTarget) changelog() string {
	return path.Join(t.dir, "CHANGELOG.md")
}

// goMod returns the module's go.mod relative to the repository root.
func (t releaseTarget) goMod() string {
	return path.Join(t.dir, "go.mod")
}

// releaseFiles lists the files a release should update, relative to the repository root.
// For the main SDK module, it's CHANGELOG.md and internal/version.go.
// For a contrib module, it's just CHANGELOG.md.
func (t releaseTarget) releaseFiles() []string {
	files := []string{t.changelog()}
	if t.versionFile != "" {
		files = append(files, t.versionFile)
	}
	return files
}

// releaseBranchName names the git branch we use for issuing a new release.
func (t releaseTarget) releaseBranchName(version semver) string {
	if t.dir == "" {
		return "chore/release-" + version.String()
	}
	return "chore/release-" + strings.ReplaceAll(t.dir, "/", "-") + "-" + version.String()
}

// releaseCommitMessage describes the release in commit messages and pull request titles.
func (t releaseTarget) releaseCommitMessage(version semver) string {
	if t.dir == "" {
		return "Prepare release " + version.String()
	}
	return "Prepare " + t.dir + " release " + version.String()
}
