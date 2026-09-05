package main

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// releaseTarget describes one independently versioned Go module in this repository.
type releaseTarget struct {
	// modulePath is the import path the module's go.mod must declare.
	modulePath string
	// tagPrefix precedes "v<version>" in the module's Go release tag, per
	// https://go.dev/ref/mod#vcs-version. It is empty for the main SDK module.
	tagPrefix string
	// versionFile holds the SDKVersion constant a release rewrites. It is empty for
	// modules that do not embed their version, whose current version comes from tags.
	versionFile string
	// markLatest reports whether GitHub should treat the release as the repository's
	// latest release. Only the main SDK module may be latest.
	markLatest bool
}

// sdkTarget describes the main go.temporal.io/sdk module at the repository root.
func sdkTarget() releaseTarget {
	return releaseTarget{
		modulePath:  "go.temporal.io/sdk",
		versionFile: "internal/version.go",
		markLatest:  true,
	}
}

// contribTarget describes a contrib module, given its directory relative to the
// repository root, such as "contrib/envconfig" or "contrib/aws/s3driver/awssdkv2".
func contribTarget(dir string) (releaseTarget, error) {
	dir = strings.Trim(strings.TrimPrefix(filepath.ToSlash(dir), "./"), "/")
	if !contribModuleRE.MatchString(dir) {
		return releaseTarget{}, fmt.Errorf(
			"invalid module %q; expected a contrib module directory such as 'contrib/envconfig'", dir)
	}
	return releaseTarget{
		modulePath: "go.temporal.io/sdk/" + dir,
		tagPrefix:  dir + "/",
		// Contrib modules are never GitHub's latest release.
	}, nil
}

// dir returns the module directory relative to the repository root, in slash form.
// For go.temporal.io/sdk, it returns the empty string;
// For go.temporal.io/sdk/contrib/envconfig, it returns "contrib/envconfig"; etc.
func (t releaseTarget) dir() string {
	if t.modulePath == "go.temporal.io/sdk" {
		return ""
	}
	return strings.TrimPrefix(t.modulePath, "go.temporal.io/sdk/")
}

// tag returns the Go module release tag for the given version.
func (t releaseTarget) tag(version string) string {
	return t.tagPrefix + "v" + version
}

// tagPattern matches every release tag belonging to this module.
func (t releaseTarget) tagPattern() string {
	return t.tagPrefix + "v*"
}

// changelogFile returns the module's changelog relative to the repository root.
func (t releaseTarget) changelogFile() string {
	return path.Join(t.dir(), "CHANGELOG.md")
}

// goModFile returns the module's go.mod relative to the repository root.
func (t releaseTarget) goModFile() string {
	return path.Join(t.dir(), "go.mod")
}

// releaseFiles returns the checked-in files a release updates, relative to the repository root.
func (t releaseTarget) releaseFiles() []string {
	files := []string{t.changelogFile()}
	if t.versionFile != "" {
		files = append(files, t.versionFile)
	}
	return files
}

// filePath resolves a repository-relative path inside the given worktree.
func (t releaseTarget) filePath(worktreeRoot, file string) string {
	return filepath.Join(worktreeRoot, filepath.FromSlash(file))
}

// releaseBranch names the branch holding the prepared release files.
func releaseBranch(args commandArgs) string {
	if args.target.modulePath == "go.temporal.io/sdk" {
		return "chore/release-" + args.version
	}
	return "chore/release-" + strings.ReplaceAll(args.target.dir(), "/", "-") + "-" + args.version
}

// releaseSubject describes the release in commit messages and pull request titles.
func releaseSubject(args commandArgs) string {
	if args.target.modulePath == "go.temporal.io/sdk" {
		return "release " + args.version
	}
	return args.target.dir() + " release " + args.version
}
