package cadence

import (
	"time"

	mock "github.com/stretchr/testify/mock"
)

// MockWorkflowEnvironment is an mock type for the workflowEnvironment type
type MockWorkflowEnvironment struct {
	mock.Mock
}

// Complete provides a mock function with given fields: result, err
func (_m *MockWorkflowEnvironment) Complete(result []byte, err Error) {
	_m.Called(result, err)
}

// ExecuteActivity provides a mock function with given fields: parameters, callback
func (_m *MockWorkflowEnvironment) ExecuteActivity(parameters ExecuteActivityParameters, callback resultHandler) {
	_m.Called(parameters, callback)
}

// NewTimer provides a mock function with given fields: timerID, delayInSeconds, callback
func (_m *MockWorkflowEnvironment) NewTimer(delayInSeconds time.Duration, callback resultHandler) {
	_m.Called(delayInSeconds, callback)
}

// Now provides a mock function with given fields:
func (_m *MockWorkflowEnvironment) Now() time.Time {
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
func (_m *MockWorkflowEnvironment) WorkflowInfo() *WorkflowInfo {
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

var _ workflowEnvironment = (*MockWorkflowEnvironment)(nil)
