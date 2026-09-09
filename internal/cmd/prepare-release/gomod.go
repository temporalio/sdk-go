package main

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

// moduleRequirement is a Temporal module that a go.mod depends on.
type moduleRequirement struct {
	modulePath string
	version    string
}

// validateGoMod requires go.mod to declare the module the release tag will version,
// and every Temporal module it depends on to use a tagged stable release instead of a
// prerelease or git snapshot.
//
// Returns the Temporal requirements, which the caller still has to check are
// published.
func validateGoMod(goMod, modulePath string) ([]moduleRequirement, error) {
	declaration := moduleDeclarationRE.FindStringSubmatch(goMod)
	if declaration == nil {
		return nil, errors.New("could not find a module declaration in go.mod")
	}
	if declaration[1] != modulePath {
		return nil, fmt.Errorf("go.mod declares module %q, expected %q", declaration[1], modulePath)
	}
	var requirements []moduleRequirement
	for _, match := range temporalModuleRequirementRE.FindAllStringSubmatch(goMod, -1) {
		required, version := match[1], match[2]
		if !goVersionRE.MatchString(version) {
			return nil, fmt.Errorf("%s must use an official release, found %q", required, version)
		}
		requirements = append(requirements, moduleRequirement{required, version})
	}
	return requirements, nil
}
