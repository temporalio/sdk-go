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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
	s := newWorkflowTestSuite()
	for i := 0; i < len(errs); i++ {
		env := s.NewTestActivityEnvironment()
		_, err := env.ExecuteActivity(errorActivityFn, i)
		require.Error(t, err)
		printError(err)
		require.Equal(t, errs[i][1], err)
	}
}

func Test_ActivityPanic(t *testing.T) {
	panicActivityFn := func() error {
		panic("panic-blabla")
	}
	s := newWorkflowTestSuite()
	s.SetLogger(zap.NewNop())
	env := s.NewTestActivityEnvironment()
	_, err := env.ExecuteActivity(panicActivityFn)
	printError(err)
	require.Error(t, err)
	panicErr, ok := err.(*PanicError)
	require.True(t, ok)
	require.Equal(t, "panic-blabla", panicErr.Error())
}

func Test_WorkflowError(t *testing.T) {
	errorWorkflowFn := func(ctx Context, i int) error {
		return errs[i][0]
	}
	s := newWorkflowTestSuite()
	for i := 0; i < len(errs); i++ {
		wfEnv := s.NewTestWorkflowEnvironment()
		wfEnv.ExecuteWorkflow(errorWorkflowFn, i)
		err := wfEnv.GetWorkflowError()
		require.Error(t, err)
		printError(err)
		require.Equal(t, errs[i][1], err)
	}

}

func printError(err error) {
	switch err := err.(type) {
	case *CanceledError:
		fmt.Printf("CanceledError: %v\n", err)
	case *TimeoutError:
		fmt.Printf("TimeoutError: %v\n", err)
	case *PanicError:
		fmt.Printf("PanicError: %v\n", err.Error())
	case *GenericError:
		fmt.Printf("GenericError: %v\n", err.Error())
	case *CustomError:
		switch err.Reason() {
		case customErrReasonA:
			var detailsMsg string
			err.Details(&detailsMsg)
			fmt.Printf("CustomError: reason:A %v\n", err)
		default:
			fmt.Printf("CustomError: unexpected reason %v\n", err)
		}
	}
}
