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

package internal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	failurepb "go.temporal.io/temporal-proto/failure"

	commonpb "go.temporal.io/temporal-proto/common"
	decisionpb "go.temporal.io/temporal-proto/decision"
	eventpb "go.temporal.io/temporal-proto/event"
	"go.uber.org/zap"
)

const (
	// assume this is some error reason defined by activity implementation.
	applicationErrReasonA = "CustomReasonA"
)

type testStruct struct {
	Name string
	Age  int
}

type testStruct2 struct {
	Name      string
	Age       int
	Favorites *[]string
}

var (
	testErrorDetails1 = "my details"
	testErrorDetails2 = 123
	testErrorDetails3 = testStruct{"a string", 321}
	testErrorDetails4 = testStruct2{"a string", 321, &[]string{"eat", "code"}}
)

func Test_GenericGoError(t *testing.T) {
	// test activity error
	errorActivityFn := func() error {
		return errors.New("error:foo")
	}
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(errorActivityFn)
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	require.IsType(t, &ApplicationError{}, err)
	require.Equal(t, "error:foo", err.Error())

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return errors.New("error:foo")
	}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflow(errorWorkflowFn)
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	require.IsType(t, &ApplicationError{}, err)
	require.Equal(t, "error:foo", err.Error())
}

func Test_ActivityNotRegistered(t *testing.T) {
	registeredActivityFn, unregisteredActivitFn := "RegisteredActivity", "UnregisteredActivityFn"
	s := &WorkflowTestSuite{}
	s.SetLogger(zap.NewNop())
	env := s.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(func() error { return nil }, RegisterActivityOptions{Name: registeredActivityFn})
	_, err := env.ExecuteActivity(unregisteredActivitFn)
	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("unable to find activityType=%v", unregisteredActivitFn))
	require.Contains(t, err.Error(), registeredActivityFn)
}

func Test_TimeoutError(t *testing.T) {
	timeoutErr := NewTimeoutError(commonpb.TimeoutType_ScheduleToStart, nil)
	require.False(t, timeoutErr.HasLastHeartbeatDetails())
	var data string
	require.Equal(t, ErrNoData, timeoutErr.LastHeartbeatDetails(&data))

	heartbeatErr := NewHeartbeatTimeoutError(testErrorDetails1)
	require.True(t, heartbeatErr.HasLastHeartbeatDetails())
	require.NoError(t, heartbeatErr.LastHeartbeatDetails(&data))
	require.Equal(t, testErrorDetails1, data)
}

func Test_TimeoutError_WithDetails(t *testing.T) {
	testTimeoutErrorDetails(t, commonpb.TimeoutType_Heartbeat)
	testTimeoutErrorDetails(t, commonpb.TimeoutType_ScheduleToClose)
	testTimeoutErrorDetails(t, commonpb.TimeoutType_StartToClose)
}

func testTimeoutErrorDetails(t *testing.T, timeoutType commonpb.TimeoutType) {
	context := &workflowEnvironmentImpl{
		decisionsHelper: newDecisionsHelper(),
		dataConverter:   getDefaultDataConverter(),
	}
	h := newDecisionsHelper()
	var actualErr error
	activityID := "activityID"
	context.decisionsHelper.scheduledEventIDToActivityID[5] = activityID
	di := h.newActivityDecisionStateMachine(
		5,
		&decisionpb.ScheduleActivityTaskDecisionAttributes{ActivityId: activityID})
	di.state = decisionStateInitiated
	di.setData(&scheduledActivity{
		callback: func(r *commonpb.Payloads, e error) {
			actualErr = e
		},
	})
	context.decisionsHelper.addDecision(di)
	encodedDetails1, _ := context.dataConverter.ToData(testErrorDetails1)
	event := createTestEventActivityTaskTimedOut(7, &eventpb.ActivityTaskTimedOutEventAttributes{
		Failure: &failurepb.Failure{
			FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{
				LastHeartbeatDetails: encodedDetails1,
				TimeoutType:          timeoutType,
			}},
		},
		RetryStatus:      commonpb.RetryStatus_Timeout,
		ScheduledEventId: 5,
		StartedEventId:   6,
	})
	weh := &workflowExecutionEventHandlerImpl{context, nil}
	_ = weh.handleActivityTaskTimedOut(event)
	var timeoutErr *TimeoutError
	ok := errors.As(actualErr, &timeoutErr)
	require.True(t, ok)
	require.True(t, timeoutErr.HasLastHeartbeatDetails())
	var data string
	require.NoError(t, timeoutErr.LastHeartbeatDetails(&data))
	require.Equal(t, testErrorDetails1, data)
}

func Test_ApplicationError(t *testing.T) {
	// test ErrorDetailValues as Details
	var a1 string
	var a2 int
	var a3 testStruct
	err0 := NewApplicationError(applicationErrReasonA, false, testErrorDetails1)
	require.True(t, err0.HasDetails())
	_ = err0.Details(&a1)
	require.Equal(t, testErrorDetails1, a1)
	a1 = ""
	err0 = NewApplicationError(applicationErrReasonA, false, testErrorDetails1, testErrorDetails2, testErrorDetails3)
	require.True(t, err0.HasDetails())
	_ = err0.Details(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	// test EncodedValues as Details
	errorActivityFn := func() error {
		return err0
	}
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(errorActivityFn)
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	err1, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err1.HasDetails())
	var b1 string
	var b2 int
	var b3 testStruct
	_ = err1.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)

	// test reason and no detail
	newReason := "another reason"
	err2 := NewApplicationError(newReason, false)
	require.True(t, !err2.HasDetails())
	require.Equal(t, ErrNoData, err2.Details())
	require.Equal(t, newReason, err2.Error())
	err3 := NewApplicationError(newReason, false, nil)
	// TODO: probably we want to handle this case when details are nil, HasDetails return false
	require.True(t, err3.HasDetails())

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return err0
	}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflow(errorWorkflowFn)
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err4, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err4.HasDetails())
	_ = err4.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)
}

func Test_ApplicationError_Pointer(t *testing.T) {
	a1 := testStruct2{}
	err1 := NewApplicationError(applicationErrReasonA, false, testErrorDetails4)
	require.True(t, err1.HasDetails())
	err := err1.Details(&a1)
	require.NoError(t, err)
	require.Equal(t, testErrorDetails4, a1)

	a2 := &testStruct2{}
	err2 := NewApplicationError(applicationErrReasonA, false, &testErrorDetails4) // // pointer in details
	require.True(t, err2.HasDetails())
	err = err2.Details(&a2)
	require.NoError(t, err)
	require.Equal(t, &testErrorDetails4, a2)

	// test EncodedValues as Details
	errorActivityFn := func() error {
		return err1
	}
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(errorActivityFn)
	_, err = env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	err3, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err3.HasDetails())
	b1 := testStruct2{}
	require.NoError(t, err3.Details(&b1))
	require.Equal(t, testErrorDetails4, b1)

	errorActivityFn2 := func() error {
		return err2 // pointer in details
	}
	env.RegisterActivity(errorActivityFn2)
	_, err = env.ExecuteActivity(errorActivityFn2)
	require.Error(t, err)
	err4, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err4.HasDetails())
	b2 := &testStruct2{}
	require.NoError(t, err4.Details(&b2))
	require.Equal(t, &testErrorDetails4, b2)

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return err1
	}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflow(errorWorkflowFn)
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err5, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err5.HasDetails())
	_ = err5.Details(&b1)
	require.NoError(t, err5.Details(&b1))
	require.Equal(t, testErrorDetails4, b1)

	errorWorkflowFn2 := func(ctx Context) error {
		return err2 // pointer in details
	}
	wfEnv = s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflow(errorWorkflowFn2)
	wfEnv.ExecuteWorkflow(errorWorkflowFn2)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err6, ok := err.(*ApplicationError)
	require.True(t, ok)
	require.True(t, err6.HasDetails())
	_ = err6.Details(&b2)
	require.NoError(t, err6.Details(&b2))
	require.Equal(t, &testErrorDetails4, b2)
}

func Test_CanceledError(t *testing.T) {
	// test ErrorDetailValues as Details
	var a1 string
	var a2 int
	var a3 testStruct
	err0 := NewCanceledError(testErrorDetails1)
	require.True(t, err0.HasDetails())
	_ = err0.Details(&a1)
	require.Equal(t, testErrorDetails1, a1)
	a1 = ""
	err0 = NewCanceledError(testErrorDetails1, testErrorDetails2, testErrorDetails3)
	require.True(t, err0.HasDetails())
	_ = err0.Details(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	// test EncodedValues as Details
	errorActivityFn := func() error {
		return err0
	}
	s := &WorkflowTestSuite{}
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(errorActivityFn)
	_, err := env.ExecuteActivity(errorActivityFn)
	require.Error(t, err)
	err1, ok := err.(*CanceledError)
	require.True(t, ok)
	require.True(t, err1.HasDetails())
	var b1 string
	var b2 int
	var b3 testStruct
	_ = err1.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)

	err2 := NewCanceledError()
	require.False(t, err2.HasDetails())

	// test workflow error
	errorWorkflowFn := func(ctx Context) error {
		return err0
	}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflow(errorWorkflowFn)
	wfEnv.ExecuteWorkflow(errorWorkflowFn)
	err = wfEnv.GetWorkflowError()
	require.Error(t, err)
	err3, ok := err.(*CanceledError)
	require.True(t, ok)
	require.True(t, err3.HasDetails())
	_ = err3.Details(&b1, &b2, &b3)
	require.Equal(t, testErrorDetails1, b1)
	require.Equal(t, testErrorDetails2, b2)
	require.Equal(t, testErrorDetails3, b3)
}

func Test_IsCanceledError(t *testing.T) {

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "empty detail",
			err:      NewCanceledError(),
			expected: true,
		},
		{
			name:     "with detail",
			err:      NewCanceledError("details"),
			expected: true,
		},
		{
			name:     "not canceled error",
			err:      errors.New("details"),
			expected: false,
		},
	}

	for _, test := range tests {
		require.Equal(t, test.expected, IsCanceledError(test.err))
	}
}

func TestErrorDetailsValues(t *testing.T) {
	e := ErrorDetailsValues{}
	require.Equal(t, ErrNoData, e.Get())

	e = ErrorDetailsValues{testErrorDetails1, testErrorDetails2, testErrorDetails3}
	var a1 string
	var a2 int
	var a3 testStruct
	require.True(t, e.HasValues())
	_ = e.Get(&a1)
	require.Equal(t, testErrorDetails1, a1)
	_ = e.Get(&a1, &a2, &a3)
	require.Equal(t, testErrorDetails1, a1)
	require.Equal(t, testErrorDetails2, a2)
	require.Equal(t, testErrorDetails3, a3)

	require.Equal(t, ErrTooManyArg, e.Get(&a1, &a2, &a3, &a3))
}

func Test_SignalExternalWorkflowExecutionFailedError(t *testing.T) {
	context := &workflowEnvironmentImpl{
		decisionsHelper: newDecisionsHelper(),
		dataConverter:   getDefaultDataConverter(),
	}
	h := newDecisionsHelper()
	var actualErr error
	var initiatedEventID int64 = 101
	signalID := "signalID"
	context.decisionsHelper.scheduledEventIDToSignalID[initiatedEventID] = signalID
	di := h.newSignalExternalWorkflowStateMachine(
		&decisionpb.SignalExternalWorkflowExecutionDecisionAttributes{},
		signalID,
	)
	di.state = decisionStateInitiated
	di.setData(&scheduledSignal{
		callback: func(r *commonpb.Payloads, e error) {
			actualErr = e
		},
	})
	context.decisionsHelper.addDecision(di)
	weh := &workflowExecutionEventHandlerImpl{context, nil}
	event := createTestEventSignalExternalWorkflowExecutionFailed(1, &eventpb.SignalExternalWorkflowExecutionFailedEventAttributes{
		InitiatedEventId: initiatedEventID,
		Cause:            eventpb.WorkflowExecutionFailedCause_UnknownExternalWorkflowExecution,
	})
	require.NoError(t, weh.handleSignalExternalWorkflowExecutionFailed(event))
	_, ok := actualErr.(*UnknownExternalWorkflowExecutionError)
	require.True(t, ok)
}

func Test_ContinueAsNewError(t *testing.T) {
	var a1 = 1234
	var a2 = "some random input"

	continueAsNewWfName := "continueAsNewWorkflowFn"
	continueAsNewWorkflowFn := func(ctx Context, testInt int, testString string) error {
		return NewContinueAsNewError(ctx, continueAsNewWfName, a1, a2)
	}

	headerValue, err := DefaultPayloadConverter.ToData("test-data")
	assert.NoError(t, err)
	header := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{"test": headerValue},
	}

	s := &WorkflowTestSuite{
		header:             header,
		contextPropagators: []ContextPropagator{NewStringMapPropagator([]string{"test"})},
	}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflowWithOptions(continueAsNewWorkflowFn, RegisterWorkflowOptions{
		Name: continueAsNewWfName,
	})
	wfEnv.ExecuteWorkflow(continueAsNewWorkflowFn, 101, "another random string")
	err = wfEnv.GetWorkflowError()

	require.Error(t, err)
	continueAsNewErr, ok := err.(*ContinueAsNewError)
	require.True(t, ok)
	require.Equal(t, continueAsNewWfName, continueAsNewErr.WorkflowType().Name)

	args := continueAsNewErr.Args()
	intArg, ok := args[0].(int)
	require.True(t, ok)
	require.Equal(t, a1, intArg)
	stringArg, ok := args[1].(string)
	require.True(t, ok)
	require.Equal(t, a2, stringArg)
	require.Equal(t, header, continueAsNewErr.params.Header)
}

func Test_GetErrorType(t *testing.T) {
	require := require.New(t)
	err := errors.New("some error")
	errType := getErrorType(err)
	require.Equal("errorString", errType)

	err = NewApplicationError("application error", false)
	errType = getErrorType(err)
	require.Equal("ApplicationError", errType)

	errType = getErrorType(nil)
	require.Equal("<nil>", errType)
}

type coolError struct{}

func (e coolError) Error() string {
	return "cool error"
}

func Test_GetErrorTypePointer(t *testing.T) {
	require := require.New(t)

	err := coolError{}
	errType := getErrorType(err)
	require.Equal("coolError", errType)

	err2 := &coolError{}
	errType2 := getErrorType(err2)
	require.Equal("coolError", errType2)
}

func Test_IsRetryable(t *testing.T) {
	require := require.New(t)
	require.False(IsRetryable(newTerminatedError(), []string{}))
	require.False(IsRetryable(NewCanceledError(), []string{}))
	require.False(IsRetryable(newWorkflowPanicError("", ""), []string{}))

	require.False(IsRetryable(NewApplicationError("", true), []string{}))
	require.True(IsRetryable(NewApplicationError("", false), []string{}))

	require.True(IsRetryable(NewTimeoutError(commonpb.TimeoutType_StartToClose, nil), []string{}))
	require.False(IsRetryable(NewTimeoutError(commonpb.TimeoutType_ScheduleToStart, nil), []string{}))
	require.False(IsRetryable(NewTimeoutError(commonpb.TimeoutType_ScheduleToClose, nil), []string{}))
	require.True(IsRetryable(NewTimeoutError(commonpb.TimeoutType_Heartbeat, nil), []string{}))

	require.False(IsRetryable(NewServerError("", true, nil), []string{}))
	require.True(IsRetryable(NewServerError("", false, nil), []string{}))

	applicationErr := &ApplicationError{originalType: "MyCoolErr"}
	require.True(IsRetryable(applicationErr, []string{}))
	require.False(IsRetryable(applicationErr, []string{"MyCoolErr"}))

	coolErr := &coolError{}
	require.True(IsRetryable(coolErr, []string{}))
	require.False(IsRetryable(coolErr, []string{"coolError"}))

	workflowExecutionErr := NewWorkflowExecutionError("", "", "", NewActivityTaskError(0, 0, "", nil, "", commonpb.RetryStatus_NonRetryableFailure, coolErr))
	require.True(IsRetryable(workflowExecutionErr, []string{}))
	require.False(IsRetryable(workflowExecutionErr, []string{"coolError"}))
}

func Test_convertErrorToFailure_ApplicationError(t *testing.T) {
	require := require.New(t)

	err := NewApplicationError("message", true, "details", 2208)
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("message", f.GetMessage())
	require.Equal("ApplicationError", f.GetApplicationFailureInfo().GetType())
	require.Equal(true, f.GetApplicationFailureInfo().GetNonRetryable())
	require.Equal([]byte(`"details"`), f.GetApplicationFailureInfo().GetDetails().GetPayloads()[0].GetData())
	require.Equal([]byte(`2208`), f.GetApplicationFailureInfo().GetDetails().GetPayloads()[1].GetData())
	require.Nil(f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var applicationErr *ApplicationError
	require.True(errors.As(err2, &applicationErr))
	require.Equal(err.Error(), applicationErr.Error())
}

func Test_convertErrorToFailure_CanceledError(t *testing.T) {
	require := require.New(t)

	err := NewCanceledError("details", 2208)
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("Canceled", f.GetMessage())
	require.Equal([]byte(`"details"`), f.GetCanceledFailureInfo().GetDetails().GetPayloads()[0].GetData())
	require.Equal([]byte(`2208`), f.GetCanceledFailureInfo().GetDetails().GetPayloads()[1].GetData())
	require.Nil(f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var canceledErr *CanceledError
	require.True(errors.As(err2, &canceledErr))
}

func Test_convertErrorToFailure_PanicError(t *testing.T) {
	require := require.New(t)

	err := newPanicError("panic message", "long call stack")
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("panic message", f.GetMessage())
	require.Equal("PanicError", f.GetApplicationFailureInfo().GetType())
	require.Equal(false, f.GetApplicationFailureInfo().GetNonRetryable())
	require.Equal("long call stack", f.GetStackTrace())
	require.Nil(f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var panicErr *PanicError
	require.True(errors.As(err2, &panicErr))
	require.Equal(err.Error(), panicErr.Error())
	require.Equal(err.StackTrace(), panicErr.StackTrace())

	f = convertErrorToFailure(newWorkflowPanicError("panic message", "long call stack"), DefaultDataConverter)
	require.Equal("panic message", f.GetMessage())
	require.Equal("PanicError", f.GetApplicationFailureInfo().GetType())
	require.Equal(true, f.GetApplicationFailureInfo().GetNonRetryable())
	require.Equal("long call stack", f.GetStackTrace())
	require.Nil(f.GetCause())

	err2 = convertFailureToError(f, DefaultDataConverter)
	require.True(errors.As(err2, &panicErr))
	require.Equal(err.Error(), panicErr.Error())
	require.Equal(err.StackTrace(), panicErr.StackTrace())
}

func Test_convertErrorToFailure_TimeoutError(t *testing.T) {
	require := require.New(t)

	err := NewTimeoutError(commonpb.TimeoutType_Heartbeat, &coolError{})
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("TimeoutType: Heartbeat, Cause: cool error", f.GetMessage())
	require.Equal(commonpb.TimeoutType_Heartbeat, f.GetTimeoutFailureInfo().GetTimeoutType())
	require.Equal(convertErrorToFailure(&coolError{}, DefaultDataConverter), f.GetCause())
	require.Equal(f.GetCause(), convertErrorToFailure(&coolError{}, DefaultDataConverter))

	err2 := convertFailureToError(f, DefaultDataConverter)
	var timeoutErr *TimeoutError
	require.True(errors.As(err2, &timeoutErr))
	require.Equal(err.Error(), timeoutErr.Error())
	require.Equal(err.TimeoutType(), timeoutErr.TimeoutType())
}

func Test_convertErrorToFailure_TerminateError(t *testing.T) {
	require := require.New(t)

	err := newTerminatedError()
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("Terminated", f.GetMessage())
	require.Nil(f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var terminateErr *TerminatedError
	require.True(errors.As(err2, &terminateErr))
}

func Test_convertErrorToFailure_ServerError(t *testing.T) {
	require := require.New(t)

	err := NewServerError("message", true, &coolError{})
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("message", f.GetMessage())
	require.Equal(true, f.GetServerFailureInfo().GetNonRetryable())
	require.Equal(convertErrorToFailure(&coolError{}, DefaultDataConverter), f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var serverErr *ServerError
	require.True(errors.As(err2, &serverErr))
	require.Equal(err.Error(), serverErr.Error())
	require.Equal(err.nonRetryable, serverErr.nonRetryable)
}

func Test_convertErrorToFailure_ActivityTaskError(t *testing.T) {
	require := require.New(t)

	applicationErr := NewApplicationError("app err", true)
	err := NewActivityTaskError(8, 22, "alex", &commonpb.ActivityType{Name: "activityType"}, "32283", commonpb.RetryStatus_NonRetryableFailure, applicationErr)
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("activity task error (scheduledEventID: 8, startedEventID: 22, identity: alex): app err", f.GetMessage())
	require.Equal(int64(8), f.GetActivityTaskFailureInfo().GetScheduledEventId())
	require.Equal(int64(22), f.GetActivityTaskFailureInfo().GetStartedEventId())
	require.Equal("alex", f.GetActivityTaskFailureInfo().GetIdentity())
	require.Equal("activityType", f.GetActivityTaskFailureInfo().GetActivityType().GetName())
	require.Equal("32283", f.GetActivityTaskFailureInfo().GetActivityId())
	require.Equal(commonpb.RetryStatus_NonRetryableFailure, f.GetActivityTaskFailureInfo().GetRetryStatus())
	require.Equal(convertErrorToFailure(applicationErr, DefaultDataConverter), f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var activityTaskErr *ActivityTaskError
	require.True(errors.As(err2, &activityTaskErr))
	require.Equal(err.Error(), activityTaskErr.Error())
	require.Equal(err.startedEventID, activityTaskErr.startedEventID)

	var applicationErr2 *ApplicationError
	require.True(errors.As(err2, &applicationErr2))
	require.Equal(applicationErr.Error(), applicationErr2.Error())
	require.Equal(applicationErr.NonRetryable(), applicationErr2.NonRetryable())
}

func Test_convertErrorToFailure_ChildWorkflowExecutionError(t *testing.T) {
	require := require.New(t)

	applicationErr := NewApplicationError("app err", true)
	err := NewChildWorkflowExecutionError("namespace", "wID", "rID", "wfType", 8, 22, commonpb.RetryStatus_NonRetryableFailure, applicationErr)
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("child workflow execution error (workflowID: wID, runID: rID, initiatedEventID: 8, startedEventID: 22, workflowType: wfType): app err", f.GetMessage())
	require.Equal(int64(8), f.GetChildWorkflowExecutionFailureInfo().GetInitiatedEventId())
	require.Equal(int64(22), f.GetChildWorkflowExecutionFailureInfo().GetStartedEventId())
	require.Equal("namespace", f.GetChildWorkflowExecutionFailureInfo().GetNamespace())
	require.Equal(commonpb.RetryStatus_NonRetryableFailure, f.GetChildWorkflowExecutionFailureInfo().GetRetryStatus())
	require.Equal(convertErrorToFailure(applicationErr, DefaultDataConverter), f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var childWorkflowExecutionErr *ChildWorkflowExecutionError
	require.True(errors.As(err2, &childWorkflowExecutionErr))
	require.Equal(err.Error(), childWorkflowExecutionErr.Error())
	require.Equal(err.startedEventID, childWorkflowExecutionErr.startedEventID)
}

func Test_convertErrorToFailure_UnknowError(t *testing.T) {
	require := require.New(t)
	err := &coolError{}
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("cool error", f.GetMessage())
	require.Equal("coolError", f.GetApplicationFailureInfo().GetType())
	require.Equal(false, f.GetApplicationFailureInfo().GetNonRetryable())
	require.Nil(f.GetCause())

	err2 := convertFailureToError(f, DefaultDataConverter)
	var coolErr *ApplicationError
	require.True(errors.As(err2, &coolErr))
	require.Equal(err.Error(), coolErr.Error())
	require.Equal("coolError", coolErr.OriginalType())
}

func Test_convertErrorToFailure_SavedFailure(t *testing.T) {
	require := require.New(t)
	err := NewApplicationError("message that will be ignored", false)
	err.originalFailure = &failurepb.Failure{
		Message:    "actual message",
		StackTrace: "some stack trace",
		Source:     "JavaSDK",
		FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
			Type:         "SomeJavaException",
			NonRetryable: true,
		}}}
	f := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("actual message", f.GetMessage())
	require.Equal("JavaSDK", f.GetSource())
	require.Equal("some stack trace", f.GetStackTrace())
	require.Equal("SomeJavaException", f.GetApplicationFailureInfo().GetType())
	require.Equal(true, f.GetApplicationFailureInfo().GetNonRetryable())
	require.Nil(f.GetCause())
}

func Test_convertFailureToError_ApplicationFailure(t *testing.T) {
	require := require.New(t)
	details, err := DefaultDataConverter.ToData("details", 22)
	assert.NoError(t, err)

	f := &failurepb.Failure{
		Message: "message",
		FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
			Type:         "ApplicationError",
			NonRetryable: true,
			Details:      details,
		}},
	}

	err = convertFailureToError(f, DefaultDataConverter)
	var applicationErr *ApplicationError
	require.True(errors.As(err, &applicationErr))

	require.Equal("message", applicationErr.Error())
	require.Equal("ApplicationError", applicationErr.OriginalType())
	require.Equal(true, applicationErr.NonRetryable())
	var str string
	var n int
	require.NoError(applicationErr.Details(&str, &n))
	require.Equal("details", str)
	require.Equal(22, n)

	f = &failurepb.Failure{
		Message:    "message",
		StackTrace: "long stack trace",
		FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
			Type: "PanicError",
		}},
	}

	err = convertFailureToError(f, DefaultDataConverter)
	var panicErr *PanicError
	require.True(errors.As(err, &panicErr))
	require.Equal("message", panicErr.Error())
	require.Equal("long stack trace", panicErr.StackTrace())

	f = &failurepb.Failure{
		Message: "message",
		FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
			Type:    "CoolError",
			Details: details,
		}},
	}

	err = convertFailureToError(f, DefaultDataConverter)
	var coolErr *ApplicationError
	require.True(errors.As(err, &coolErr))
	require.Equal("message", coolErr.Error())
	require.Equal("CoolError", coolErr.OriginalType())
	require.Equal(false, coolErr.NonRetryable())
}

func Test_convertFailureToError_CanceledFailure(t *testing.T) {
	require := require.New(t)

	details, err := DefaultDataConverter.ToData("details", 22)
	assert.NoError(t, err)

	f := &failurepb.Failure{
		FailureInfo: &failurepb.Failure_CanceledFailureInfo{CanceledFailureInfo: &failurepb.CanceledFailureInfo{
			Details: details,
		}},
	}

	err = convertFailureToError(f, DefaultDataConverter)
	var canceledErr *CanceledError
	require.True(errors.As(err, &canceledErr))
	var str string
	var n int
	require.NoError(canceledErr.Details(&str, &n))
	require.Equal("details", str)
	require.Equal(22, n)
}

func Test_convertFailureToError_TimeoutFailure(t *testing.T) {
	require := require.New(t)
	f := &failurepb.Failure{
		FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{
			TimeoutType:          commonpb.TimeoutType_Heartbeat,
			LastHeartbeatDetails: nil,
		}},
	}

	err := convertFailureToError(f, DefaultDataConverter)
	var timeoutErr *TimeoutError
	require.True(errors.As(err, &timeoutErr))
	require.Equal("TimeoutType: Heartbeat, Cause: <nil>", timeoutErr.Error())
	require.Equal(commonpb.TimeoutType_Heartbeat, timeoutErr.TimeoutType())
}

func Test_convertFailureToError_ServerFailure(t *testing.T) {
	require := require.New(t)
	f := &failurepb.Failure{
		Message: "message",
		FailureInfo: &failurepb.Failure_ServerFailureInfo{ServerFailureInfo: &failurepb.ServerFailureInfo{
			NonRetryable: true,
		}},
	}

	err := convertFailureToError(f, DefaultDataConverter)
	var serverErr *ServerError
	require.True(errors.As(err, &serverErr))
	require.Equal("message", serverErr.Error())
	require.Equal(true, serverErr.nonRetryable)
}

func Test_convertFailureToError_SaveFailure(t *testing.T) {
	require := require.New(t)

	f := &failurepb.Failure{
		Message:    "message",
		StackTrace: "long stack trace",
		Source:     "JavaSDK",
		Cause: &failurepb.Failure{
			Message:    "application message",
			StackTrace: "application long stack trace",
			Source:     "JavaSDK",
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
				Type:         "SomeJavaException",
				NonRetryable: true,
			}},
		},
		FailureInfo: &failurepb.Failure_ActivityTaskFailureInfo{ActivityTaskFailureInfo: &failurepb.ActivityTaskFailureInfo{
			StartedEventId:   1,
			ScheduledEventId: 2,
			Identity:         "alex",
		}},
	}

	err := convertFailureToError(f, DefaultDataConverter)

	var applicationErr *ApplicationError
	require.True(errors.As(err, &applicationErr))
	require.NotNil(applicationErr.originalFailure)
	applicationErr.message = "errors are immutable, message can't be changed"
	applicationErr.originalType = "ApplicationError (is ignored)"
	applicationErr.nonRetryable = false

	var activityErr *ActivityTaskError
	require.True(errors.As(err, &activityErr))
	require.NotNil(activityErr.originalFailure)
	activityErr.startedEventID = 11
	activityErr.scheduledEventID = 22
	activityErr.identity = "bob"

	f2 := convertErrorToFailure(err, DefaultDataConverter)
	require.Equal("message", f2.GetMessage())
	require.Equal("long stack trace", f2.GetStackTrace())
	require.Equal("JavaSDK", f2.GetSource())
	require.Equal(int64(1), f2.GetActivityTaskFailureInfo().GetStartedEventId())
	require.Equal(int64(2), f2.GetActivityTaskFailureInfo().GetScheduledEventId())
	require.Equal("alex", f2.GetActivityTaskFailureInfo().GetIdentity())

	require.Equal("application message", f2.GetCause().GetMessage())
	require.Equal("application long stack trace", f2.GetCause().GetStackTrace())
	require.Equal("JavaSDK", f2.GetCause().GetSource())
	require.Equal("SomeJavaException", f2.GetCause().GetApplicationFailureInfo().GetType())
	require.Equal(true, f2.GetCause().GetApplicationFailureInfo().GetNonRetryable())
}
