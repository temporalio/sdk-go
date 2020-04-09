// Package testsuite contains unit testing framework for Temporal workflows and activities.
package testsuite

import (
	"go.temporal.io/temporal/internal"
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
)

// ErrMockStartChildWorkflowFailed is special error used to indicate the mocked child workflow should fail to start.
var ErrMockStartChildWorkflowFailed = internal.ErrMockStartChildWorkflowFailed
