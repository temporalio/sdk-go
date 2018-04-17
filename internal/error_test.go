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
	"go.uber.org/zap"
	"testing"
)

const (
	// assume this is some error reason defined by activity implementation.
	customErrReasonA = "CustomReasonA"
)

var errs = [][]error{
	// pairs of errors: actualErrorActivityReturns and expectedErrorDeciderSees
	{errors.New("error:foo"), &GenericError{"error:foo"}},
	{NewCustomError(customErrReasonA, "my details"), NewCustomError(customErrReasonA, "my details")},
	// assume "reason:X" is some new reason activity could return, but workflow code was not aware of.
	{NewCustomError("reason:X"), NewCustomError("reason:X")},
	{NewCanceledError("some-details"), NewCanceledError("some-details")},
}

func Test_ActivityError(t *testing.T) {
	errorActivityFn := func(i int) error {
		return errs[i][0]
	}
	RegisterActivity(errorActivityFn)
	s := &WorkflowTestSuite{}
	for i := 0; i < len(errs); i++ {
		env := s.NewTestActivityEnvironment()
		_, err := env.ExecuteActivity(errorActivityFn, i)
		require.Error(t, err)
		require.Equal(t, errs[i][1], err)
	}
}

func Test_ActivityPanic(t *testing.T) {
	panicActivityFn := func() error {
		panic("panic-blabla")
	}
	RegisterActivity(panicActivityFn)
	s := &WorkflowTestSuite{}
	s.SetLogger(zap.NewNop())
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(panicActivityFn)
	require.Error(t, err)
	panicErr, ok := err.(*PanicError)
	require.True(t, ok)
	require.Equal(t, "panic-blabla", panicErr.Error())
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

func Test_WorkflowError(t *testing.T) {
	errorWorkflowFn := func(ctx Context, i int) error {
		return errs[i][0]
	}
	RegisterWorkflow(errorWorkflowFn)
	s := &WorkflowTestSuite{}
	for i := 0; i < len(errs); i++ {
		wfEnv := s.NewTestWorkflowEnvironment()
		wfEnv.ExecuteWorkflow(errorWorkflowFn, i)
		err := wfEnv.GetWorkflowError()
		require.Error(t, err)
		require.Equal(t, errs[i][1], err)
	}
}

func Test_ErrorDetails(t *testing.T) {
	timeoutErr := NewTimeoutError(shared.TimeoutTypeScheduleToStart)
	require.False(t, timeoutErr.HasDetails())
	var data string
	require.Equal(t, ErrNoData, timeoutErr.Details(&data))

	heartbeatErr := NewHeartbeatTimeoutError("detailed-info")
	require.True(t, heartbeatErr.HasDetails())
	require.NoError(t, heartbeatErr.Details(&data))
	require.Equal(t, "detailed-info", data)
}
