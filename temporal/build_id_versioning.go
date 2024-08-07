// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package temporal

import "go.temporal.io/sdk/internal"

// VersioningIntent indicates whether the user intends certain commands to be run on
// a compatible worker build ID version or not.
// WARNING: Worker versioning is currently experimental
type VersioningIntent = internal.VersioningIntent

const (
	// VersioningIntentUnspecified indicates that the SDK should choose the most sensible default
	// behavior for the type of command, accounting for whether the command will be run on the same
	// task queue as the current worker.
	// WARNING: Worker versioning is currently experimental
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
	// WARNING: Worker versioning is currently experimental
	VersioningIntentInheritBuildID = internal.VersioningIntentInheritBuildID
	// VersioningIntentUseAssignmentRules indicates the command should use the latest Assignment Rules
	// to select a Build ID independently of the workflow triggering it.
	// This is the default behavior for commands not running on the same Task Queue as the current worker.
	// WARNING: Worker versioning is currently experimental
	VersioningIntentUseAssignmentRules = internal.VersioningIntentUseAssignmentRules
)
