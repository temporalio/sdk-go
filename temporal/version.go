package temporal

import "go.temporal.io/sdk/internal"

// SDKVersion is a semver that represents the version of this Temporal SDK.
// This represents API changes visible to Temporal SDK consumers, i.e. developers
// that are writing workflows. So every time we change API that can affect them we have to change this number.
// Format: MAJOR.MINOR.PATCH
const SDKVersion = internal.SDKVersion
