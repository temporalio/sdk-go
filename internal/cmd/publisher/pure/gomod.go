package pure

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	// Matches a go.mod module declaration like "module go.temporal.io/contrib/envconfig".
	moduleDeclarationRE = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	// Matches go.temporal.io dependency strings like "require go.temporal.io/sdk v1.48.0" or "go.temporal.io/sdk v1.48.0".
	temporalModuleRequirementRE = regexp.MustCompile(`(?m)^\s*(?:require\s+)?(go\.temporal\.io/\S+)\s+(v\S+)\s*(?://.*)?$`)
	// Matches tagged Go module release versions such as "v1.48.0".
	// Shouldn't match pseudo-versions such as "v1.48.0-0.20260804123456-abcdef123456".
	goVersionRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// GoMod contains the module path and validated Temporal requirements from a go.mod file.
type GoMod struct {
	ModulePath   string
	Requirements []ModuleRequirement
}

// ModuleRequirement is a Temporal module that a go.mod depends on.
type ModuleRequirement struct {
	ModulePath string
	Version    string
}

// NewValidatedGoMod requires go.mod to declare the module the release tag will version,
// and every Temporal module it depends on to use a tagged stable release instead of a
// prerelease or git snapshot.
func NewValidatedGoMod(goMod, modulePath string) (GoMod, error) {
	declaration := moduleDeclarationRE.FindStringSubmatch(goMod)
	if declaration == nil {
		return GoMod{}, errors.New("could not find a module declaration in go.mod")
	}
	if declaration[1] != modulePath {
		return GoMod{}, fmt.Errorf("go.mod declares module %q, expected %q", declaration[1], modulePath)
	}
	var requirements []ModuleRequirement
	for _, match := range temporalModuleRequirementRE.FindAllStringSubmatch(goMod, -1) {
		required, version := match[1], match[2]
		if !goVersionRE.MatchString(version) {
			return GoMod{}, fmt.Errorf("%s must use an official release, found %q", required, version)
		}
		requirements = append(requirements, ModuleRequirement{required, version})
	}
	return GoMod{ModulePath: modulePath, Requirements: requirements}, nil
}

// SortGoMods partitions modules into the minimum number of release groups.
// Dependencies in the input must be in earlier groups; other requirements are ignored.
// Each module goes in the group after its deepest dependency, so the number of
// groups equals the longest dependency chain. Modules within a group retain input order.
func SortGoMods(modules []GoMod) ([][]GoMod, error) {
	byPath := make(map[string]int, len(modules))
	for i, module := range modules {
		if module.ModulePath == "" {
			return nil, fmt.Errorf("go.mod at index %d has no module path", i)
		}
		if _, exists := byPath[module.ModulePath]; exists {
			return nil, fmt.Errorf("duplicate module path %q", module.ModulePath)
		}
		byPath[module.ModulePath] = i
	}

	const (
		visiting = 1
		visited  = 2
	)
	state := make([]int, len(modules))
	group := make([]int, len(modules))
	var visit func(int) error
	visit = func(i int) error {
		switch state[i] {
		case visiting:
			return fmt.Errorf("dependency cycle involving module %q", modules[i].ModulePath)
		case visited:
			return nil
		}
		state[i] = visiting
		for _, requirement := range modules[i].Requirements {
			if dependency, exists := byPath[requirement.ModulePath]; exists {
				if err := visit(dependency); err != nil {
					return err
				}
				group[i] = max(group[i], group[dependency]+1)
			}
		}
		state[i] = visited
		return nil
	}
	for i := range modules {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	var partitions [][]GoMod
	for i, module := range modules {
		for len(partitions) <= group[i] {
			partitions = append(partitions, nil)
		}
		partitions[group[i]] = append(partitions[group[i]], module)
	}
	return partitions, nil
}
