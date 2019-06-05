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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/encoded"
)

type SessionTestSuite struct {
	*require.Assertions
	suite.Suite
	WorkflowTestSuite
	sessionOptions *SessionOptions
}

func (s *SessionTestSuite) SetupSuite() {
	RegisterActivityWithOptions(testSessionActivity, RegisterActivityOptions{Name: "testSessionActivity"})
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

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestCreationWithOpenSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			tasklist:     "some random tasklist",
			sessionState: sessionStateOpen,
		})
		_, err := CreateSession(sessionCtx, s.sessionOptions)
		return err
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Equal(errFoundExistingOpenSession.Error(), env.GetWorkflowError().Error())
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
			tasklist:     "some random tasklist",
			sessionState: sessionStateClosed,
		})

		sessionCtx, err := CreateSession(sessionCtx, s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
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
			tasklist:     "some random tasklist",
			sessionState: sessionStateFailed,
		})

		sessionCtx, err := CreateSession(sessionCtx, s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestCompletionWithClosedSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			tasklist:     "some random tasklist",
			sessionState: sessionStateClosed,
		})
		CompleteSession(sessionCtx)
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestCompletionWithFailedSessionContext() {
	workflowFn := func(ctx Context) error {
		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			tasklist:     "some random tasklist",
			sessionState: sessionStateFailed,
		})
		CompleteSession(sessionCtx)
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestGetSessionInfo() {
	workflowFn := func(ctx Context) error {
		info := GetSessionInfo(ctx)
		if info != nil {
			return errors.New("GetSessionInfo should return nil when there's no session info")
		}

		sessionCtx := setSessionInfo(ctx, &SessionInfo{
			SessionID:    "some random sessionID",
			tasklist:     "some random tasklist",
			sessionState: sessionStateFailed,
		})
		info = GetSessionInfo(sessionCtx)
		if info == nil {
			return errors.New("returned session info should not be nil")
		}

		newSessionInfo := &SessionInfo{
			SessionID:    "another sessionID",
			tasklist:     "another tasklist",
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

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
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
			tasklist:     "some random tasklist",
			sessionState: sessionStateFailed,
		}

		sessionCtx, err := RecreateSession(ctx, sessionInfo.GetRecreateToken(), s.sessionOptions)
		if err != nil {
			return err
		}
		CompleteSession(sessionCtx)
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SessionTestSuite) TestMaxConcurrentSession_CreationOnly() {
	maxConCurrentSessionExecutionSize := 3
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)
		for i := 0; i != maxConCurrentSessionExecutionSize+1; i++ {
			if _, err := s.createSessionWithoutRetry(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{
		MaxConCurrentSessionExecutionSize: maxConCurrentSessionExecutionSize,
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Equal(errTooManySessionsMsg, env.GetWorkflowError().Error())
}

func (s *SessionTestSuite) TestMaxConcurrentSession_WithRecreation() {
	maxConCurrentSessionExecutionSize := 3
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
			return errors.New("Returned session info should not be nil")
		}

		for i := 0; i != maxConCurrentSessionExecutionSize; i++ {
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

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{
		MaxConCurrentSessionExecutionSize: maxConCurrentSessionExecutionSize,
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Equal(errTooManySessionsMsg, env.GetWorkflowError().Error())
}

func (s *SessionTestSuite) TestSessionTaskList() {
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

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	taskListUsed := []string{}
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
		taskListUsed = append(taskListUsed, activityInfo.TaskList)
	})
	resourceID := "testResourceID"
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(getCreationTasklist(defaultTestTaskList), taskListUsed[0])
	expectedTaskList := getResourceSpecificTasklist(resourceID)
	for _, taskList := range taskListUsed[1:] {
		s.Equal(expectedTaskList, taskList)
	}
}

func (s *SessionTestSuite) TestSessionRecreationTaskList() {
	numActivities := 3
	resourceID := "testResourceID"
	resourceSpecificTaskList := getResourceSpecificTasklist(resourceID)
	workflowFn := func(ctx Context) error {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
			HeartbeatTimeout:       time.Second * 20,
		}
		ctx = WithActivityOptions(ctx, ao)

		sessionInfo := &SessionInfo{
			SessionID:    "testSessionID",
			tasklist:     resourceSpecificTaskList,
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

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	taskListUsed := []string{}
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
		taskListUsed = append(taskListUsed, activityInfo.TaskList)
	})
	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	for _, taskList := range taskListUsed {
		s.Equal(resourceSpecificTaskList, taskList)
	}
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
			tasklist:     "random tasklist",
			sessionState: sessionStateFailed,
		})

		return ExecuteActivity(sessionCtx, testSessionActivity, "a random name").Get(sessionCtx, nil)
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.Equal(ErrSessionFailed.Error(), env.GetWorkflowError().Error())
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
			tasklist:     "random tasklist",
			sessionState: sessionStateClosed,
		})

		return ExecuteActivity(sessionCtx, testSessionActivity, "some random message").Get(sessionCtx, nil)
	}

	RegisterWorkflow(workflowFn)
	env := s.NewTestWorkflowEnvironment()
	var taskListUsed string
	env.SetOnActivityStartedListener(func(activityInfo *ActivityInfo, ctx context.Context, args encoded.Values) {
		taskListUsed = activityInfo.TaskList
	})

	env.ExecuteWorkflow(workflowFn)

	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.Equal(defaultTestTaskList, taskListUsed)
}

func (s *SessionTestSuite) TestSessionRecreateToken() {
	testTasklist := "some random tasklist"

	sessionInfo := &SessionInfo{
		SessionID:    "testSessionID",
		tasklist:     tasklist,
		sessionState: sessionStateClosed,
	}
	token := sessionInfo.GetRecreateToken()
	params, err := deserializeRecreateToken(token)
	s.NoError(err)
	s.Equal(testTasklist, params.Tasklist)
}

func (s *SessionTestSuite) createSessionWithoutRetry(ctx Context) (Context, error) {
	options := getActivityOptions(ctx)
	baseTasklist := options.TaskListName
	if baseTasklist == "" {
		baseTasklist = options.OriginalTaskListName
	}
	return createSession(ctx, getCreationTasklist(baseTasklist), s.sessionOptions, false)
}

func testSessionActivity(ctx context.Context, name string) (string, error) {
	return "Hello" + name + "!", nil
}
