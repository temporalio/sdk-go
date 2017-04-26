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

var _ workflowEnvironment = (*mockWorkflowEnvironment)(nil)
