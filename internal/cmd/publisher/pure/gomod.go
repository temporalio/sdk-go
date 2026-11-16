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

// GoMod contains the validated Temporal requirements from a go.mod file.
type GoMod struct {
	Requirements []ModuleRequirement
}

// ModuleRequirement is a Temporal module that a go.mod depends on.
type ModuleRequirement struct {
	ModulePath string
	Version    string
}

// ValidateModulePath checks that go.mod declares the module the release tag will version.
func ValidateModulePath(goMod, modulePath string) error {
	declaration := moduleDeclarationRE.FindStringSubmatch(goMod)
	if declaration == nil {
		return errors.New("could not find a module declaration in go.mod")
	}
	if declaration[1] != modulePath {
		return fmt.Errorf("go.mod declares module %q, expected %q", declaration[1], modulePath)
	}
	return nil
}

// NewValidatedGoMod requires go.mod to declare the module the release tag will version,
// and every Temporal module it depends on to use a tagged stable release instead of a
// prerelease or git snapshot.
//
// The caller still has to check that the returned Temporal requirements are published.
func NewValidatedGoMod(goMod, modulePath string) (GoMod, error) {
	err := ValidateModulePath(goMod, modulePath)
	if err != nil {
		return GoMod{}, err
	}
	var requirements []ModuleRequirement
	for _, match := range temporalModuleRequirementRE.FindAllStringSubmatch(goMod, -1) {
		required, version := match[1], match[2]
		if !goVersionRE.MatchString(version) {
			return GoMod{}, fmt.Errorf("%s must use an official release, found %q", required, version)
		}
		requirements = append(requirements, ModuleRequirement{required, version})
	}
	return GoMod{Requirements: requirements}, nil
}
