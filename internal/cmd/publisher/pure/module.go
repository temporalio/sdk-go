package pure

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// SDKModulePath is the main SDK module's import path.
const SDKModulePath = "go.temporal.io/sdk"

// Matches relative paths of contrib module directories like "contrib/envconfig"
// and "contrib/aws/s3driver/awssdkv2".
var contribModuleRE = regexp.MustCompile(`^contrib(/[a-z0-9][a-z0-9._-]*)+$`)

// Module describes a Go module in this repository.
type Module struct {
	// Dir is the module directory relative to the repository root in slash form,
	// e.g. "contrib/envconfig". Empty for the main SDK module.
	Dir string
}

// VersionedModule describes a module and the version to release.
type VersionedModule struct {
	Module  Module
	Version Version
}

// SDKModule describes the main go.temporal.io/sdk module.
func SDKModule() Module {
	return Module{}
}

// ContribModule describes a contrib module, given its directory relative to the
// repository root, like "contrib/envconfig" or "contrib/aws/s3driver/awssdkv2".
// Requires UNIX slash syntax.
func ContribModule(dir string) (Module, error) {
	dir = strings.TrimSuffix(strings.TrimPrefix(dir, "./"), "/")
	if !contribModuleRE.MatchString(dir) {
		return Module{}, fmt.Errorf(
			"invalid module %q; expected a slash-separated contrib module directory such as 'contrib/envconfig'", dir)
	}
	return Module{Dir: dir}, nil
}

// ContribModules returns contrib modules identified by go.mod files, sorted by
// directory, whether or not they have changelogs. Paths must be repository-relative
// and slash-separated.
func ContribModules(paths []string) []Module {
	files := make(map[string]bool)
	for _, file := range paths {
		files[file] = true
	}
	var modules []Module
	for file := range files {
		if path.Base(file) != "go.mod" {
			continue
		}
		module, err := ContribModule(path.Dir(file))
		if err == nil {
			modules = append(modules, module)
		}
	}
	slices.SortFunc(modules, func(a, b Module) int {
		return strings.Compare(a.Dir, b.Dir)
	})
	return modules
}

// ParseVersionedModule parses a MODULE=VERSION assignment, with 'sdk' naming the Go SDK.
func ParseVersionedModule(arg string) (VersionedModule, error) {
	dir, version, ok := strings.Cut(arg, "=")
	if !ok {
		return VersionedModule{}, fmt.Errorf("invalid release %q; expected MODULE=VERSION, such as 'sdk=1.48.0'", arg)
	}
	m := SDKModule()
	var err error
	if dir != "sdk" {
		m, err = ContribModule(dir)
		if err != nil {
			return VersionedModule{}, err
		}
	}
	parsed, err := ParseVersion(version)
	if err != nil {
		return VersionedModule{}, err
	}
	return VersionedModule{Module: m, Version: parsed}, nil
}

// ParseVersionedModules parses MODULE=VERSION assignments, rejecting duplicate modules.
func ParseVersionedModules(args []string) ([]VersionedModule, error) {
	var modules []VersionedModule
	seen := make(map[string]bool)
	for _, arg := range args {
		module, err := ParseVersionedModule(arg)
		if err != nil {
			return nil, err
		}
		if seen[module.Module.Dir] {
			return nil, fmt.Errorf("module %q is specified more than once", module.Module.ModulePath())
		}
		seen[module.Module.Dir] = true
		modules = append(modules, module)
	}
	return modules, nil
}

// FormatVersionedModules formats modules as space-separated MODULE=VERSION assignments.
func FormatVersionedModules(modules []VersionedModule) string {
	var args []string
	for _, module := range modules {
		args = append(args, module.Module.Name()+"="+module.Version.String())
	}
	return strings.Join(args, " ")
}

// Name returns "sdk" for the main SDK module or the contrib module's directory.
func (m Module) Name() string {
	if m.Dir == "" {
		return "sdk"
	}
	return m.Dir
}

// ModulePath returns the module path, e.g. "go.temporal.io/sdk/contrib/envconfig".
func (m Module) ModulePath() string {
	return path.Join(SDKModulePath, m.Dir)
}

// TagPrefix returns what precedes "v<version>" in the module's Go release tag.
// For the main SDK module, it's empty.
// For a contrib module like "contrib/envconfig", it's "contrib/envconfig/".
func (m Module) TagPrefix() string {
	if m.Name() == "sdk" {
		return ""
	}
	return m.Dir + "/"
}

// Tag returns the Go module release tag for the given version.
func (m Module) Tag(version Version) string {
	return m.TagPrefix() + "v" + version.String()
}

// TagPattern is a glob pattern that matches release tags belonging to this module.
func (m Module) TagPattern() string {
	return m.TagPrefix() + "v*"
}

// Changelog returns the module's changelog path. An empty prefix returns a
// repository-relative slash path; otherwise prefix must be the absolute repository root.
func (m Module) Changelog(prefix string) string {
	if prefix == "" {
		return path.Join(m.Dir, "CHANGELOG.md")
	}
	return filepath.Join(prefix, filepath.FromSlash(m.Dir), "CHANGELOG.md")
}

// Fragments returns the module's pending changelog directory. An empty prefix returns
// a repository-relative slash path; otherwise prefix must be the absolute repository root.
func (m Module) Fragments(prefix string) string {
	if prefix == "" {
		return path.Join(m.Dir, "changelog")
	}
	return filepath.Join(prefix, filepath.FromSlash(m.Dir), "changelog")
}

// GoMod returns the module's go.mod path. An empty prefix returns a
// repository-relative slash path; otherwise prefix must be the absolute repository root.
func (m Module) GoMod(prefix string) string {
	if prefix == "" {
		return path.Join(m.Dir, "go.mod")
	}
	return filepath.Join(prefix, filepath.FromSlash(m.Dir), "go.mod")
}

// VersionFile returns the module's version file path and whether the module has one,
// without checking the filesystem. An empty prefix returns a repository-relative
// slash path; otherwise prefix must be the absolute repository root.
func (m Module) VersionFile(prefix string) (string, bool) {
	if m.Name() != "sdk" {
		return "", false
	}
	if prefix == "" {
		return "internal/version.go", true
	}
	return filepath.Join(prefix, "internal", "version.go"), true
}

// MarkLatest reports whether GitHub should mark the release as the repository's latest release.
func (m Module) MarkLatest() bool {
	return m.Name() == "sdk"
}

// GenerateNotes reports whether GitHub should populate draft releases with --generate-notes.
func (m Module) GenerateNotes() bool {
	return m.Name() == "sdk"
}

// ReleaseFiles lists the files a release should update. An empty prefix returns
// repository-relative slash paths; otherwise prefix must be the absolute repository root.
// For the main SDK module, it's CHANGELOG.md and internal/version.go.
// For a contrib module, it's just CHANGELOG.md.
func (m Module) ReleaseFiles(prefix string) []string {
	files := []string{m.Changelog(prefix)}
	if versionFile, ok := m.VersionFile(prefix); ok {
		files = append(files, versionFile)
	}
	return files
}

// ReleaseBranchName names the git branch we use for issuing a new release.
func ReleaseBranchName(releaseDate time.Time) string {
	return "chore/release-" + releaseDate.Format(time.DateOnly)
}

// ReleaseCommitMessage describes the release date and modules in a commit or PR.
func ReleaseCommitMessage(modules []VersionedModule, releaseDate time.Time) string {
	return "Prepare release " + releaseDate.Format(time.DateOnly) + "\n\n" + FormatVersionedModules(modules)
}
