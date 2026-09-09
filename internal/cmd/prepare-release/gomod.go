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

// validateGoMod requires go.mod to declare the module the release tag will version,
// and every Temporal module it depends on to use a tagged stable release instead of a
// prerelease or git snapshot. A module path mismatch means the module directory is not
// the one the caller named.
func validateGoMod(goMod, modulePath string) error {
	declaration := moduleDeclarationRE.FindStringSubmatch(goMod)
	if declaration == nil {
		return errors.New("could not find a module declaration in go.mod")
	}
	if declaration[1] != modulePath {
		return fmt.Errorf("go.mod declares module %q, expected %q", declaration[1], modulePath)
	}
	for _, requirement := range temporalModuleRequirementRE.FindAllStringSubmatch(goMod, -1) {
		if !goVersionRE.MatchString(requirement[2]) {
			return fmt.Errorf("%s must use an official release, found %q", requirement[1], requirement[2])
		}
	}
	return nil
}
