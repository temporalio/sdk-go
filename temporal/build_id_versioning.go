package temporal

import "go.temporal.io/sdk/internal"

// VersioningIntent indicates whether the user intends certain commands to be run on
// a compatible worker build ID version or not.
//
// Deprecated: Build-id based versioning is deprecated in favor of worker deployment based versioning and will be removed soon.
//
// WARNING: Worker versioning is currently experimental
//
//lint:ignore SA1019 ignore for SDK
type VersioningIntent = internal.VersioningIntent

const (
	// VersioningIntentUnspecified indicates that the SDK should choose the most sensible default
	// behavior for the type of command, accounting for whether the command will be run on the same
	// task queue as the current worker.
	//
	// Deprecated: This has the same effect as [VersioningIntentInheritBuildID], use that instead.
	//
	// WARNING: Worker versioning is currently experimental
	//lint:ignore SA1019 ignore for SDK
	VersioningIntentUnspecified = internal.VersioningIntentUnspecified
	// VersioningIntentCompatible indicates that the command should run on a worker with compatible
	// version if possible. It may not be possible if the target task queue does not also have
	// knowledge of the current worker's build ID.
	//
	// Deprecated: This has the same effect as [VersioningIntentInheritBuildID], use that instead.
	//lint:ignore SA1019 ignore for SDK
	VersioningIntentCompatible = internal.VersioningIntentCompatible
	// VersioningIntentDefault indicates that the command should run on the target task queue's
	// current overall-default build ID.
	//
	// Deprecated: This has the same effect as [VersioningIntentUseAssignmentRules], use that instead.
	//lint:ignore SA1019 ignore for SDK
	VersioningIntentDefault = internal.VersioningIntentDefault
	// VersioningIntentInheritBuildID indicates the command should inherit the current Build ID of the
	// Workflow triggering it, and not use Assignment Rules. (Redirect Rules are still applicable)
	// This is the default behavior for commands running on the same Task Queue as the current worker.
	//
	// Deprecated: This has the same effect as [VersioningIntentInheritBuildID], use that instead.
	//
	// WARNING: Worker versioning is currently experimental
	//lint:ignore SA1019 ignore for SDK
	VersioningIntentInheritBuildID = internal.VersioningIntentInheritBuildID
	// VersioningIntentUseAssignmentRules indicates the command should use the latest Assignment Rules
	// to select a Build ID independently of the workflow triggering it.
	// This is the default behavior for commands not running on the same Task Queue as the current worker.
	//
	// Deprecated: This has the same effect as [VersioningIntentInheritBuildID], use that instead.
	//
	// WARNING: Worker versioning is currently experimental
	//lint:ignore SA1019 ignore for SDK
	VersioningIntentUseAssignmentRules = internal.VersioningIntentUseAssignmentRules
)
