package pure

import (
	"fmt"
	"path"
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
	// VersionFile is "internal/version.go" if we're releasing the main SDK module.
	// Otherwise it's an empty string.
	VersionFile string
	// MarkLatest is whether GitHub should mark the release as the repository's
	// latest release.
	MarkLatest bool
	// GenerateNotes is whether GitHub should populate draft releases with
	// --generate-notes.
	GenerateNotes bool
}

// VersionedModule describes a module and the version to release.
type VersionedModule struct {
	Module  Module
	Version Version
}

// SDKModule describes the main go.temporal.io/sdk module.
func SDKModule() Module {
	return Module{
		VersionFile:   "internal/version.go",
		MarkLatest:    true,
		GenerateNotes: true,
	}
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
	return Module{
		Dir:           dir,
		MarkLatest:    false,
		GenerateNotes: false,
	}, nil
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
		dir := module.Module.Dir
		if dir == "" {
			dir = "sdk"
		}
		args = append(args, dir+"="+module.Version.String())
	}
	return strings.Join(args, " ")
}

// ModulePath returns the module path, e.g. "go.temporal.io/sdk/contrib/envconfig".
func (m Module) ModulePath() string {
	return path.Join(SDKModulePath, m.Dir)
}

// TagPrefix returns what precedes "v<version>" in the module's Go release tag.
// For the main SDK module, it's empty.
// For a contrib module like "contrib/envconfig", it's "contrib/envconfig/".
func (m Module) TagPrefix() string {
	if m.Dir == "" {
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

// Changelog returns the module's changelog relative to the repository root.
func (m Module) Changelog() string {
	return path.Join(m.Dir, "CHANGELOG.md")
}

// GoMod returns the module's go.mod relative to the repository root.
func (m Module) GoMod() string {
	return path.Join(m.Dir, "go.mod")
}

// ReleaseFiles lists the files a release should update, relative to the repository root.
// For the main SDK module, it's CHANGELOG.md and internal/version.go.
// For a contrib module, it's just CHANGELOG.md.
func (m Module) ReleaseFiles() []string {
	files := []string{m.Changelog()}
	if m.VersionFile != "" {
		files = append(files, m.VersionFile)
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
