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

package internal

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/zap"
	"testing"
)

const (
	// assume this is some error reason defined by activity implementation.
	customErrReasonA = "CustomReasonA"
)

type testStruct struct {
	Name string
	Age  int
}

var (
	testErrorDetails1 = "my details"
	testErrorDetails2 = 123
	testErrorDetails3 = testStruct{"a string", 321}
)

func Test_GenericError(t *testing.T) {
	// test activity error
	errorActivityFn := func() error {
		return errors.New("error:foo")
	}
	RegisterActivity(errorActivityFn)
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	require.Equal(t, &GenericError{"error:foo"}, err)

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return errors.New("error:foo")
	}
	RegisterWorkflow(errorWorkflowFn)
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	require.Equal(t, &GenericError{"error:foo"}, err)
}

func Test_ActivityNotRegistered(t *testing.T) {
	registeredActivityFn, unregisteredActivitFn := "RegisteredActivity", "UnregisteredActivityFn"
	RegisterActivityWithOptions(func() error { return nil }, RegisterActivityOptions{Name: registeredActivityFn})
	s := &WorkflowTestSuite{}
	s.SetLogger(zap.NewNop())
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(unregisteredActivitFn)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("unable to find activityType=%v", unregisteredActivitFn))
	require.Contains(t, err.Error(), registeredActivityFn)
}

func Test_TimeoutError(t *testing.T) {
	timeoutErr := NewTimeoutError(shared.TimeoutTypeScheduleToStart)
	require.False(t, timeoutErr.HasDetails())
	var data string
	require.Equal(t, ErrNoData, timeoutErr.Details(&data))

	heartbeatErr := NewHeartbeatTimeoutError(testErrorDetails1)
	require.True(t, heartbeatErr.HasDetails())
	require.NoError(t, heartbeatErr.Details(&data))
	require.Equal(t, testErrorDetails1, data)

	// test heartbeatTimeout inside internal_event_handlers
	context := &workflowEnvironmentImpl{
		decisionsHelper: newDecisionsHelper(),
		dataConverter:   newDefaultDataConverter(),
	}
	var actualErr error
	activityID := "activityID"
	context.decisionsHelper.scheduledEventIDToActivityID[5] = activityID
	di := newActivityDecisionStateMachine(
		&shared.ScheduleActivityTaskDecisionAttributes{ActivityId: common.StringPtr(activityID)})
	di.state = decisionStateInitiated
	di.setData(&scheduledActivity{
		callback: func(r []byte, e error) {
			actualErr = e
		},
	})
	context.decisionsHelper.decisions[makeDecisionID(decisionTypeActivity, activityID)] = di
	timeoutType := shared.TimeoutTypeHeartbeat
	encodedDetails1, _ := context.dataConverter.ToData(testErrorDetails1)
	event := createTestEventActivityTaskTimedOut(7, &shared.ActivityTaskTimedOutEventAttributes{
		Details:          encodedDetails1,
		ScheduledEventId: common.Int64Ptr(5),
		StartedEventId:   common.Int64Ptr(6),
		TimeoutType:      &timeoutType,
	})
	weh := &workflowExecutionEventHandlerImpl{context, nil}
	weh.handleActivityTaskTimedOut(event)
	err, ok := actualErr.(*TimeoutError)
	require.True(t, ok)
	require.True(t, err.HasDetails())
	data = ""
	require.NoError(t, err.Details(&data))
	require.Equal(t, testErrorDetails1, data)
}

func Test_CustomError(t *testing.T) {
	// test ErrorDetailValues as Details
	var a1 string
	var a2 int
	var a3 testStruct
	err0 := NewCustomError(customErrReasonA, testErrorDetails1)
	require.True(t, err0.HasDetails())
	err0.Details(&a1)
	require.Equal(t, testErrorDetails1, a1)
	a1 = ""
	err0 = NewCustomError(customErrReasonA, testErrorDetails1, testErrorDetails2, testErrorDetails3)
	require.True(t, err0.HasDetails())
	err0.Details(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	// test EncodedValues as Details
	errorActivityFn := func() error {
		return err0
	}
	RegisterActivity(errorActivityFn)
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	err1, ok := err.(*CustomError)
	require.True(t, ok)
	require.True(t, err1.HasDetails())
	var b1 string
	var b2 int
	var b3 testStruct
	err1.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)

	// test reason and no detail
	require.Panics(t, func() { NewCustomError("cadenceInternal:testReason") })
	newReason := "another reason"
	err2 := NewCustomError(newReason)
	require.True(t, !err2.HasDetails())
	require.Equal(t, ErrNoData, err2.Details())
	require.Equal(t, newReason, err2.Reason())
	err3 := NewCustomError(newReason, nil)
	// TODO: probably we want to handle this case when details are nil, HasDetails return false
	require.True(t, err3.HasDetails())

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return err0
	}
	RegisterWorkflow(errorWorkflowFn)
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err4, ok := err.(*CustomError)
	require.True(t, ok)
	require.True(t, err4.HasDetails())
	err4.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)
}

func Test_CanceledError(t *testing.T) {
	// test ErrorDetailValues as Details
	var a1 string
	var a2 int
	var a3 testStruct
	err0 := NewCanceledError(testErrorDetails1)
	require.True(t, err0.HasDetails())
	err0.Details(&a1)
	require.Equal(t, testErrorDetails1, a1)
	a1 = ""
	err0 = NewCanceledError(testErrorDetails1, testErrorDetails2, testErrorDetails3)
	require.True(t, err0.HasDetails())
	err0.Details(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	// test EncodedValues as Details
	errorActivityFn := func() error {
		return err0
	}
	RegisterActivity(errorActivityFn)
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	err1, ok := err.(*CanceledError)
	require.True(t, ok)
	require.True(t, err1.HasDetails())
	var b1 string
	var b2 int
	var b3 testStruct
	err1.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)

	err2 := NewCanceledError()
	require.False(t, err2.HasDetails())

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return err0
	}
	RegisterWorkflow(errorWorkflowFn)
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err3, ok := err.(*CanceledError)
	require.True(t, ok)
	require.True(t, err3.HasDetails())
	err3.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)
}

func TestErrorDetailsValues(t *testing.T) {
	e := ErrorDetailsValues{}
	require.Equal(t, ErrNoData, e.Get())

	e = ErrorDetailsValues{testErrorDetails1, testErrorDetails2, testErrorDetails3}
	var a1 string
	var a2 int
	var a3 testStruct
	require.True(t, e.HasValues())
	e.Get(&a1)
	require.Equal(t, testErrorDetails1, a1)
	e.Get(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	require.Equal(t, ErrTooManyArg, e.Get(&a1, &a2, &a3, &a3))
}
