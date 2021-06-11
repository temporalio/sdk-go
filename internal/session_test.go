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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"go.temporal.io/sdk/converter"
)

type SessionTestSuite struct {
	*require.Assertions
	suite.Suite
	WorkflowTestSuite
	sessionOptions *SessionOptions
}

func (s *SessionTestSuite) SetupSuite() {
	s.sessionOptions = &SessionOptions{
		ExecutionTimeout: time.Minute,
		CreationTimeout:  time.Minute,
	}
}

func (s *SessionTestSuite) SetupTest() {
	s.Assertions = require.New(s.T())
}

func TestSessionTestSuite(t *testing.T) {
	suite.Run(t, new(SessionTestSuite))
}

func (s *SessionTestSuite) TestCreationCompletion() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}
		info := GetSessionInfo(sessionCtx)
		if info == nil || info.sessionState != sessionStateOpen {
			return errors.New("session state should be open after creation")
		}

		CompleteSession(sessionCtx)

		info = GetSessionInfo(sessionCtx)
		if info == nil || info.sessionState != sessionStateClosed {
			return errors.New("session state should be closed after completion")
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestCreationWithOpenSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateOpen,
		})
		_, err := CreateSession(sessionCtx, s.sessionOptions)
		return err
	}

	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)

	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var err1 *ApplicationError
	s.True(errors.As(err, &err1))

	s.Equal(errFoundExistingOpenSession.Error(), err1.Error())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestCreationWithClosedSessionContext() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateClosed,
		})

		sessionCtx, err := CreateSession(sessionCtx, s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestCreationWithFailedSessionContext() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateFailed,
		})

		sessionCtx, err := CreateSession(sessionCtx, s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestCompletionWithClosedSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateClosed,
		})
		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestCompletionWithFailedSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateFailed,
		})
		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestGetSessionInfo() {
	workflowFn := func(ctx Context) error {
		info := GetSessionInfo(ctx)
		if info != nil {
			return errors.New("GetSessionInfo should return nil when there's no session info")
		}

		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateFailed,
		})
		info = GetSessionInfo(sessionCtx)
		if info == nil {
			return errors.New("returned session info should not be nil")
		}

		newSessionInfo := &SessionInfo{
			SessionID:    "another sessionID",
			taskqueue:    "another taskqueue",
			sessionState: sessionStateClosed,
		}
		sessionCtx = setSessionInfo(ctx, newSessionInfo)
		info = GetSessionInfo(sessionCtx)
		if info == nil {
			return errors.New("returned session info should not be nil")
		}
		if info != newSessionInfo {
			return errors.New("GetSessionInfo should return info for the most recent session in the context")
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestRecreation() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionInfo := &SessionInfo{
			SessionID:    "some random sessionID",
			taskqueue:    "some random taskqueue",
			sessionState: sessionStateFailed,
		}

		sessionCtx, err := RecreateSession(ctx, sessionInfo.GetRecreateToken(), s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestMaxConcurrentSession_CreationOnly() {
	maxConcurrentSessionExecutionSize := 3
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		for i := 0; i != maxConcurrentSessionExecutionSize+1; i++ {
			if _, err := s.createSessionWithoutRetry(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.SetWorkerOptions(WorkerOptions{
		MaxConcurrentSessionExecutionSize: maxConcurrentSessionExecutionSize,
		EnableSessionWorker:               true,
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var err1 *ApplicationError
	s.True(errors.As(err, &err1))
	s.Equal(errTooManySessionsMsg, err1.Error())
}

func (s *SessionTestSuite) TestMaxConcurrentSession_WithRecreation() {
	maxConcurrentSessionExecutionSize := 3
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}
		sessionInfo := GetSessionInfo(sessionCtx)
		if sessionInfo == nil {
			return errors.New("returned session info should not be nil")
		}

		for i := 0; i != maxConcurrentSessionExecutionSize; i++ {
			if i%2 == 0 {
				_, err = s.createSessionWithoutRetry(ctx)
			} else {
				_, err = RecreateSession(ctx, sessionInfo.GetRecreateToken(), s.sessionOptions)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.SetWorkerOptions(WorkerOptions{
		MaxConcurrentSessionExecutionSize: maxConcurrentSessionExecutionSize,
		EnableSessionWorker:               true,
	})
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var err1 *ApplicationError
	s.True(errors.As(err, &err1))

	s.Equal(errTooManySessionsMsg, err1.Error())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestSessionTaskQueue() {
	numActivities := 3
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}

		for i := 0; i != numActivities; i++ {
			if err := ExecuteActivity(sessionCtx, testSessionActivity, "a random name").Get(sessionCtx, nil); err != nil {
				return err
			}
		}

		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterActivity(testSessionActivity)

	var taskQueueUsed []string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args converter.EncodedValues) {
		taskQueueUsed = append(taskQueueUsed, activityInfo.TaskQueue)
	})
	resourceID := "testResourceID"
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(getCreationTaskqueue(defaultTestTaskQueue), taskQueueUsed[0])
	expectedTaskQueue := getResourceSpecificTaskqueue(resourceID)
	for _, taskQueue := range taskQueueUsed[1:] {
		s.Equal(expectedTaskQueue, taskQueue)
	}
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestSessionRecreationTaskQueue() {
	numActivities := 3
	resourceID := "testResourceID"
	resourceSpecificTaskQueue := getResourceSpecificTaskqueue(resourceID)
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)

		sessionInfo := &SessionInfo{
			SessionID:    "testSessionID",
			taskqueue:    resourceSpecificTaskQueue,
			sessionState: sessionStateClosed,
		}
		sessionCtx, err := RecreateSession(ctx, sessionInfo.GetRecreateToken(), s.sessionOptions)
		if err != nil {
			return err
		}

		for i := 0; i != numActivities; i++ {
			if err := ExecuteActivity(sessionCtx, testSessionActivity, "a random name").Get(sessionCtx, nil); err != nil {
				return err
			}
		}

		CompleteSession(sessionCtx)
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterActivity(testSessionActivity)

	var taskQueueUsed []string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args converter.EncodedValues) {
		taskQueueUsed = append(taskQueueUsed, activityInfo.TaskQueue)
	})
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(sessionCompletionActivity).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	for _, taskQueue := range taskQueueUsed {
		s.Equal(resourceSpecificTaskQueue, taskQueue)
	}
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestExecuteActivityInFailedSession() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "random sessionID",
			taskqueue:    "random taskqueue",
			sessionState: sessionStateFailed,
		})

		return ExecuteActivity(sessionCtx, testSessionActivity, "a random name").Get(sessionCtx, nil)
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	s.Error(err)
	var workflowErr *WorkflowExecutionError
	s.True(errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var err1 *ApplicationError
	s.True(errors.As(err, &err1))

	s.Equal(ErrSessionFailed.Error(), err1.Error())
}

func (s *SessionTestSuite) TestExecuteActivityInClosedSession() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "random sessionID",
			taskqueue:    "random taskqueue",
			sessionState: sessionStateClosed,
		})

		return ExecuteActivity(sessionCtx, testSessionActivity, "some random message").Get(sessionCtx, nil)
	}

	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflowFn)
	env.RegisterActivity(testSessionActivity)
	var taskQueueUsed string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args converter.EncodedValues) {
		taskQueueUsed = activityInfo.TaskQueue
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(defaultTestTaskQueue, taskQueueUsed)
}

func (s *SessionTestSuite) TestSessionRecreateToken() {
	testTaskqueue := "some random taskqueue"

	sessionInfo := &SessionInfo{
		SessionID:    "testSessionID",
		taskqueue:    taskqueue,
		sessionState: sessionStateClosed,
	}
	token := sessionInfo.GetRecreateToken()
	params, err := deserializeRecreateToken(token)
	s.NoError(err)
	s.Equal(testTaskqueue, params.Taskqueue)
}

func (s *SessionTestSuite) TestInvalidRecreateToken() {
	token := []byte("some invalid token")
	sessionCtx, err := RecreateSession(Background(), token, s.sessionOptions)
	s.Error(err)
	s.Nil(sessionCtx)
}

func (s *SessionTestSuite) TestCompletionFailed() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}

		CompleteSession(sessionCtx)

		info := GetSessionInfo(sessionCtx)
		if info == nil || info.sessionState != sessionStateClosed {
			return errors.New("session state should be closed after completion even when completion activity failed")
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.OnActivity(sessionCreationActivityName, mock.Anything, mock.Anything).Return(sessionCreationActivity).Once()
	// fail the activity and return non retryable error to avoid retry.
	env.OnActivity(sessionCompletionActivityName, mock.Anything, mock.Anything).Return(NewApplicationError("some error", "", true, nil)).Once()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	env.AssertExpectations(s.T())
}

func (s *SessionTestSuite) TestUserTimerWithinSession() {
	workflowFn := func(ctx Context) error {
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}

		defer CompleteSession(sessionCtx)

		if err := NewTimer(sessionCtx, time.Hour).Get(sessionCtx, nil); err != nil {
			return err
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterWorkflow(workflowFn)
	env.ExecuteWorkflow(workflowFn)

	env.AssertExpectations(s.T())
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestActivityRetryWithinSession() {
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToCloseTimeout: 10 * time.Minute,
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
			RetryPolicy: &RetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    0,
			},
		}
		ctx = WithActivityOptions(ctx, ao)
		sessionCtx, err := CreateSession(ctx, s.sessionOptions)
		if err != nil {
			return err
		}

		defer CompleteSession(sessionCtx)

		if err := ExecuteActivity(sessionCtx, testSessionActivity, "").Get(sessionCtx, nil); err != nil {
			return err
		}
		return nil
	}

	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{EnableSessionWorker: true})
	env.RegisterActivity(testSessionActivity)
	env.OnActivity(testSessionActivity, mock.Anything, mock.Anything).Return("", errors.New("some random error"))
	env.ExecuteWorkflow(workflowFn)

	env.AssertExpectations(s.T())
	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
}

func (s *SessionTestSuite) createSessionWithoutRetry(ctx Context) (Context, error) {
	options := getActivityOptions(ctx)
	baseTaskqueue := options.TaskQueueName
	if baseTaskqueue == "" {
		baseTaskqueue = options.OriginalTaskQueueName
	}
	return createSession(ctx, getCreationTaskqueue(baseTaskqueue), s.sessionOptions, false)
}

func testSessionActivity(_ context.Context, name string) (string, error) {
	return "Hello" + name + "!", nil
}
