// Package testsuite contains unit testing framework for Temporal workflows and activities and a helper to download and
// start a dev server.
package testsuite

import (
	"go.temporal.io/sdk/internal"
)

type (
	// WorkflowTestSuite is the test suite to run unit tests for workflow/activity.
	WorkflowTestSuite = internal.WorkflowTestSuite

	// TestWorkflowEnvironment is the environment that you use to test workflow
	TestWorkflowEnvironment = internal.TestWorkflowEnvironment

	// TestActivityEnvironment is the environment that you use to test activity
	TestActivityEnvironment = internal.TestActivityEnvironment

	// MockCallWrapper is a wrapper to mock.Call. It offers the ability to wait on workflow's clock instead of wall clock.
	MockCallWrapper = internal.MockCallWrapper

	// TestUpdateCallback is a basic implementation of the UpdateCallbacks interface for testing purposes.
	TestUpdateCallback = internal.TestUpdateCallback
)

// ErrMockStartChildWorkflowFailed is special error used to indicate the mocked child workflow should fail to start.
var ErrMockStartChildWorkflowFailed = internal.ErrMockStartChildWorkflowFailed
