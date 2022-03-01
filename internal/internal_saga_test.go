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

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

type SagaTestSuite struct {
	suite.Suite
	WorkflowTestSuite
}

func TestSagaTestSuite(t *testing.T) {
	suite.Run(t, new(SagaTestSuite))
}

func (s *SagaTestSuite) Test_SagaWorkflow_Success() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var count int
	s.NoError(env.GetWorkflowResult(&count))
	s.Equal(60, count)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_Failure() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(0, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Equal([]int{20, 10}, compensationOrder)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_Failure_SagaCancel() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      false,
		RunCancelFunction:    true,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(30, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Equal([]int{}, compensationOrder)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_WorkflowCancelled() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(func(ctx context.Context, n int) error {
		env.CancelWorkflow()
		return nil
	})

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): canceled")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(0, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Equal([]int{20, 10}, compensationOrder)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_CompensationFail() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.OnActivity(w.minus, mock.Anything, 20).Return(errors.New("Test ContinueOnError"))

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(30, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Equal([]int{}, compensationOrder)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_ContinueOnError() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.OnActivity(w.minus, mock.Anything, 20).Return(errors.New("Test ContinueOnError"))
	env.OnActivity(w.minus, mock.Anything, 10).Return(w.minus)

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: false,
		ContinueOnError:      true,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(20, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Equal([]int{10}, compensationOrder)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_Parallel() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: true,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(0, count)

	result, err = env.QueryWorkflow("compensationOrder")
	s.NoError(err)

	var compensationOrder []int
	err = result.Get(&compensationOrder)
	s.NoError(err)
	s.Len(compensationOrder, 2)

	env.AssertExpectations(s.T())
}

func (s *SagaTestSuite) Test_SagaWorkflow_ParallelFailure() {
	env := s.NewTestWorkflowEnvironment()
	w := &testSagaWorkflow{
		Count:             0,
		CompensationOrder: []int{},
	}
	env.RegisterActivity(w.add)
	env.RegisterActivity(w.minus)

	env.OnActivity(w.add, mock.Anything, 10).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 20).Return(w.add)
	env.OnActivity(w.add, mock.Anything, 30).Return(errors.New("Test Compensation"))

	env.OnActivity(w.minus, mock.Anything, 20).Return(w.minus)
	env.OnActivity(w.minus, mock.Anything, 10).Return(errors.New("Test Parallel Failure"))

	env.ExecuteWorkflow(w.run, sagaTestOptions{
		ParallelCompensation: true,
		ContinueOnError:      false,
	})

	s.True(env.IsWorkflowCompleted())
	s.EqualError(env.GetWorkflowError(), "workflow execution error (type: run, workflowID: default-test-workflow-id, runID: default-test-run-id): activity error (type: add, scheduledEventID: 0, startedEventID: 0, identity: ): Test Compensation")

	result, err := env.QueryWorkflow("count")
	s.NoError(err)

	var count int
	err = result.Get(&count)
	s.NoError(err)
	s.Equal(10, count)

	env.AssertExpectations(s.T())
}

// Everything below is a basic workflow used for unit testing the Saga functionality
type testSagaWorkflow struct {
	Count             int
	CompensationOrder []int
	mu                sync.Mutex
}

type sagaTestOptions struct {
	ParallelCompensation bool
	ContinueOnError      bool
	RunCancelFunction    bool
}

func (w *testSagaWorkflow) run(ctx Context, opts sagaTestOptions) (res int, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
	}
	ctx = WithActivityOptions(ctx, ao)

	options := &SagaOptions{
		ParallelCompensation: opts.ParallelCompensation,
		ContinueOnError:      opts.ContinueOnError,
	}

	saga := NewSaga(WithSagaOptions(ctx, options))
	defer func() {
		if opts.RunCancelFunction {
			saga.Cancel()
		}
		if err != nil {
			_ = saga.Compensate()
		}
	}()

	err = SetQueryHandler(ctx, "count", func(input []byte) (int, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.Count, nil
	})
	if err != nil {
		return -1, err
	}

	err = SetQueryHandler(ctx, "compensationOrder", func(input []byte) ([]int, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.CompensationOrder, nil
	})
	if err != nil {
		return -1, err
	}

	err = ExecuteActivity(ctx, w.add, 10).Get(ctx, nil)
	if err != nil {
		return -1, err
	}
	saga.AddCompensation(w.minus, 10)

	err = ExecuteActivity(ctx, w.add, 20).Get(ctx, nil)
	if err != nil {
		return -1, err
	}
	saga.AddCompensation(w.minus, 20)

	err = ExecuteActivity(ctx, w.add, 30).Get(ctx, nil)
	if err != nil {
		return -1, err
	}

	return w.Count, nil
}

func (w *testSagaWorkflow) add(ctx context.Context, n int) error {
	w.Count += n
	return nil
}

func (w *testSagaWorkflow) minus(ctx context.Context, n int) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Count -= n
	w.CompensationOrder = append(w.CompensationOrder, n)
	return nil
}
