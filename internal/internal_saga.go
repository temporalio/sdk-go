// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2022 Uber Technologies, Inc.
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

// All code in this file is private to the package.

import (
	"fmt"
	"strings"
)

const sagaOptionsContextKey = "sagaOptions"

func getSagaOptions(ctx Context) *SagaOptions {
	so := ctx.Value(sagaOptionsContextKey)
	if so == nil {
		return nil
	}
	return so.(*SagaOptions)
}

func setSagaParametersIfNotExist(ctx Context) Context {
	params := getSagaOptions(ctx)
	var newParams SagaOptions
	if params != nil {
		newParams = *params
	}

	return WithValue(ctx, sagaOptionsContextKey, &newParams)
}

type compensationOp struct {
	activity interface{}
	args     []interface{}
}

// Saga implements the logic to execute compensation operations
// https://en.wikipedia.org/wiki/Compensating_transaction that is
// often required in Saga applications.
type Saga struct {
	ctx               Context
	options           *SagaOptions
	compensationStack []*compensationOp
	cancelFunc        CancelFunc

	// since the cancelFunc is non-blocking, we use a canceled to ensure
	// compensation steps aren't run after the cancelFunc is called.
	canceled bool
}

// Cancel cancels the current Saga.
func (s *Saga) Cancel() {
	s.cancelFunc()
	s.canceled = true
}

// AddCompensation adds a compensation step to the stack.
func (s *Saga) AddCompensation(activity interface{}, args ...interface{}) {
	s.compensationStack = append(s.compensationStack, &compensationOp{
		activity: activity,
		args:     args,
	})
}

// Compensate executes all the compensation operations in the stack.
// After the first call, subsequent calls to a Compensate do nothing.
func (s *Saga) Compensate() error {
	// first check if Cancel was called to prevent compensation race.
	if s.canceled {
		return ErrCanceled
	}

	compensationFn := s.compensateSequential
	if s.options.ParallelCompensation {
		compensationFn = s.compensateParallel
	}

	return compensationFn()
}

func (s *Saga) compensateParallel() error {
	var (
		futures         []Future
		compensationErr = new(CompensationError)
	)

	for _, op := range s.compensationStack {
		futures = append(futures, ExecuteActivity(s.ctx, op.activity, op.args...))
	}

	for _, future := range futures {
		err := future.Get(s.ctx, nil)
		if err != nil {
			if !s.options.ContinueOnError {
				return err
			}

			compensationErr.addError(err)
		}
	}
	if compensationErr.hasErrors() {
		return compensationErr
	}

	return nil
}

func (s *Saga) compensateSequential() error {
	compensationErr := new(CompensationError)

	for i := len(s.compensationStack) - 1; i >= 0; i-- {
		op := s.compensationStack[i]

		err := ExecuteActivity(s.ctx, op.activity, op.args...).Get(s.ctx, nil)
		if err != nil {
			if !s.options.ContinueOnError {
				return err
			}

			compensationErr.addError(err)
		}
	}
	return nil
}

// CompensationError implements the error interface and aggregates
// the compensation errors that occur during execution
type CompensationError struct {
	Errors []error
}

func (e *CompensationError) addError(err error) {
	e.Errors = append(e.Errors, err)
}

func (e *CompensationError) hasErrors() bool {
	return len(e.Errors) > 0
}

// Error implements the error interface
// It returns the concatenated error messages
func (e *CompensationError) Error() string {
	if !e.hasErrors() {
		return ""
	}

	var errors []string
	for _, err := range e.Errors {
		errors = append(errors, err.Error())
	}
	return fmt.Sprintf("error(s) in saga compensation: %s", strings.Join(errors, "; "))
}
