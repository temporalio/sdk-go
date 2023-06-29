// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
)

// ErrMockStartChildWorkflowFailed is special error used to indicate the mocked child workflow should fail to start.
var ErrMockStartChildWorkflowFailed = internal.ErrMockStartChildWorkflowFailed
