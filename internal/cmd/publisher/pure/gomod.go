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

// SortGoMods topologically sorts modules and then greedily partitions
// that order into consecutive groups with no dependencies within a group.
// For example, given modules C B A D E, with dependencies A → B → C and
// E → D, we'd first produce the topological sort [C, B, A, D, E] and then
// group them as [[C], [B], [A, D], [E]] because C -> B and A -> B and E -> D.
// Notice A and D are grouped because module A doesn't require D.
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
	sorted := make([]GoMod, 0, len(modules))
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
			}
		}
		state[i] = visited
		sorted = append(sorted, modules[i])
		return nil
	}
	for i := range modules {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	var partitions [][]GoMod
	var partition []GoMod
	members := make(map[string]bool)
	for _, module := range sorted {
		for _, requirement := range module.Requirements {
			if members[requirement.ModulePath] {
				partitions = append(partitions, partition)
				partition = nil
				clear(members)
				break
			}
		}
		partition = append(partition, module)
		members[module.ModulePath] = true
	}
	if len(partition) > 0 {
		partitions = append(partitions, partition)
	}
	return partitions, nil
}
