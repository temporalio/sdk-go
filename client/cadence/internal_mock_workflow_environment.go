// Copyright (c) 2017 Uber Technologies, Inc.
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

package cadence

import (
	"time"

	mock "github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

// mockWorkflowEnvironment is an mock type for the workflowEnvironment type
type mockWorkflowEnvironment struct {
	mock.Mock
}

// RequestCancelActivity provides a mock function with given fields: activityID
func (_m *mockWorkflowEnvironment) RequestCancelActivity(activityID string) {
	_m.Called(activityID)
}

// RequestCancelTimer provides a mock function with given fields: timerID
func (_m *mockWorkflowEnvironment) RequestCancelTimer(timerID string) {
	_m.Called(timerID)
}

// Complete provides a mock function with given fields: result, err
func (_m *mockWorkflowEnvironment) Complete(result []byte, err error) {
	_m.Called(result, err)
}

// GetLogger provides a mock function with to return a logger
func (_m *mockWorkflowEnvironment) GetLogger() *zap.Logger {
	ret := _m.Called()

	var r0 *zap.Logger
	if rf, ok := ret.Get(0).(func() *zap.Logger); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*zap.Logger)
		}
	}

	return r0
}

// ExecuteActivity provides a mock function with given fields: parameters, callback
func (_m *mockWorkflowEnvironment) ExecuteActivity(parameters executeActivityParameters, callback resultHandler) *activityInfo {
	ret := _m.Called(parameters, callback)

	var r0 *activityInfo
	if rf, ok := ret.Get(0).(func(executeActivityParameters, resultHandler) *activityInfo); ok {
		r0 = rf(parameters, callback)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*activityInfo)
		}
	}

	return r0
}

// NewTimer provides a mock function with given fields: d, callback
func (_m *mockWorkflowEnvironment) NewTimer(d time.Duration, callback resultHandler) *timerInfo {
	ret := _m.Called(d, callback)

	var r0 *timerInfo
	if rf, ok := ret.Get(0).(func(time.Duration, resultHandler) *timerInfo); ok {
		r0 = rf(d, callback)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*timerInfo)
		}
	}

	return r0
}

// Now provides a mock function with given fields:
func (_m *mockWorkflowEnvironment) Now() time.Time {
	ret := _m.Called()

	var r0 time.Time
	if rf, ok := ret.Get(0).(func() time.Time); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Time)
	}

	return r0
}

// WorkflowInfo provides a mock function with given fields:
func (_m *mockWorkflowEnvironment) WorkflowInfo() *WorkflowInfo {
	ret := _m.Called()

	var r0 *WorkflowInfo
	if rf, ok := ret.Get(0).(func() *WorkflowInfo); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*WorkflowInfo)
		}
	}

	return r0
}

// RequestCancelWorkflow provides a mock function with given fields: domainName, workflowID, runID
func (_m *mockWorkflowEnvironment) RequestCancelWorkflow(domainName string, workflowID string, runID string) error {
	ret := _m.Called(domainName, workflowID, runID)

	var r0 error
	if rf, ok := ret.Get(0).(func(string, string, string) error); ok {
		r0 = rf(domainName, workflowID, runID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RegisterCancel provides a mock function with given fields: handler
func (_m *mockWorkflowEnvironment) RegisterCancel(handler func()) {
	_m.Called(handler)
}

var _ workflowEnvironment = (*mockWorkflowEnvironment)(nil)
