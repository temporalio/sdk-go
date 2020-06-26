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
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/temporal-proto/common/v1"
	enumspb "go.temporal.io/temporal-proto/enums/v1"
	historypb "go.temporal.io/temporal-proto/history/v1"
	namespacepb "go.temporal.io/temporal-proto/namespace/v1"
	"go.temporal.io/temporal-proto/serviceerror"
	taskqueuepb "go.temporal.io/temporal-proto/taskqueue/v1"
	"go.temporal.io/temporal-proto/workflowservice/v1"
	"go.temporal.io/temporal-proto/workflowservicemock/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

func testInternalWorkerRegister(r *registry) {
	r.RegisterWorkflowWithOptions(
		sampleWorkflowExecute,
		RegisterWorkflowOptions{Name: "sampleWorkflowExecute"},
	)
	r.RegisterWorkflow(testReplayWorkflow)
	r.RegisterWorkflow(testReplayWorkflowLocalActivity)
	r.RegisterWorkflow(testReplayWorkflowFromFile)
	r.RegisterWorkflow(testReplayWorkflowFromFileParent)
	r.RegisterActivityWithOptions(
		testActivity,
		RegisterActivityOptions{Name: "testActivity"},
	)
	r.RegisterActivity(testActivityByteArgs)
	r.RegisterActivityWithOptions(
		testActivityMultipleArgs,
		RegisterActivityOptions{Name: "testActivityMultipleArgs"},
	)
	r.RegisterActivity(testActivityMultipleArgsWithStruct)
	r.RegisterActivity(testActivityReturnString)
	r.RegisterActivity(testActivityReturnEmptyString)
	r.RegisterActivity(testActivityReturnEmptyStruct)

	r.RegisterActivity(testActivityNoResult)
	r.RegisterActivity(testActivityNoContextArg)
	r.RegisterActivity(testActivityReturnByteArray)
	r.RegisterActivity(testActivityReturnInt)
	r.RegisterActivity(testActivityReturnNilStructPtr)
	r.RegisterActivity(testActivityReturnStructPtr)
	r.RegisterActivity(testActivityReturnNilStructPtrPtr)
	r.RegisterActivity(testActivityReturnStructPtrPtr)
}

func testInternalWorkerRegisterWithTestEnv(env *TestWorkflowEnvironment) {
	env.RegisterWorkflowWithOptions(
		sampleWorkflowExecute,
		RegisterWorkflowOptions{Name: "sampleWorkflowExecute"},
	)
	env.RegisterWorkflow(testReplayWorkflow)
	env.RegisterWorkflow(testReplayWorkflowLocalActivity)
	env.RegisterWorkflow(testReplayWorkflowFromFile)
	env.RegisterWorkflow(testReplayWorkflowFromFileParent)
	env.RegisterActivityWithOptions(
		testActivity,
		RegisterActivityOptions{Name: "testActivity"},
	)
	env.RegisterActivity(testActivityByteArgs)
	env.RegisterActivityWithOptions(
		testActivityMultipleArgs,
		RegisterActivityOptions{Name: "testActivityMultipleArgs"},
	)
	env.RegisterActivity(testActivityMultipleArgsWithStruct)
	env.RegisterActivity(testActivityReturnString)
	env.RegisterActivity(testActivityReturnEmptyString)
	env.RegisterActivity(testActivityReturnEmptyStruct)

	env.RegisterActivity(testActivityNoResult)
	env.RegisterActivity(testActivityNoContextArg)
	env.RegisterActivity(testActivityReturnByteArray)
	env.RegisterActivity(testActivityReturnInt)
	env.RegisterActivity(testActivityReturnNilStructPtr)
	env.RegisterActivity(testActivityReturnStructPtr)
	env.RegisterActivity(testActivityReturnNilStructPtrPtr)
	env.RegisterActivity(testActivityReturnStructPtrPtr)
}

type internalWorkerTestSuite struct {
	suite.Suite
	mockCtrl      *gomock.Controller
	service       *workflowservicemock.MockWorkflowServiceClient
	registry      *registry
	dataConverter DataConverter
}

func TestInternalWorkerTestSuite(t *testing.T) {
	s := &internalWorkerTestSuite{
		registry:      newRegistry(),
		dataConverter: getDefaultDataConverter(),
	}
	testInternalWorkerRegister(s.registry)
	suite.Run(t, s)
}

func (s *internalWorkerTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *internalWorkerTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *internalWorkerTestSuite) createLocalActivityMarkerDataForTest(activityID string) map[string]*commonpb.Payloads {
	lamd := localActivityMarkerData{
		ActivityID: activityID,
		ReplayTime: time.Now(),
	}

	// encode marker data
	markerData, err := s.dataConverter.ToPayloads(lamd)
	s.NoError(err)

	return map[string]*commonpb.Payloads{
		localActivityMarkerDataDetailsName:   markerData,
		localActivityMarkerResultDetailsName: {},
	}
}

func getLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func testReplayWorkflow(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowLocalActivity(ctx Context) error {
	ao := LocalActivityOptions{
		ScheduleToCloseTimeout: time.Second,
	}
	ctx = WithLocalActivityOptions(ctx, ao)
	err := ExecuteLocalActivity(ctx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteLocalActivity(ctx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowFromFile(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       20 * time.Second,
		WaitForCancellation:    true,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivityMultipleArgs", 2, "test", true).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func testReplayWorkflowFromFileParent(ctx Context) error {
	execution := GetWorkflowInfo(ctx).WorkflowExecution
	childID := fmt.Sprintf("child_workflow:%v", execution.RunID)
	cwo := ChildWorkflowOptions{
		WorkflowID:               childID,
		WorkflowExecutionTimeout: time.Minute,
	}
	ctx = WithChildWorkflowOptions(ctx, cwo)
	var result string
	cwf := ExecuteChildWorkflow(ctx, testReplayWorkflowFromFile)
	f1 := cwf.SignalChildWorkflow(ctx, "test-signal", "test-data")
	err := f1.Get(ctx, nil)
	if err != nil {
		return err
	}
	err = cwf.Get(ctx, &result)
	if err != nil {
		return err
	}
	return nil
}

func testActivity(context.Context) error {
	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflow"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "5",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(6, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 5,
		}),
		createTestEventActivityTaskCompleted(7, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 5,
			StartedEventId:   6,
		}),
		createTestEventDecisionTaskScheduled(8, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(9),
		createTestEventDecisionTaskCompleted(10, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 8,
			StartedEventId:   9,
		}),
		createTestEventWorkflowExecutionCompleted(11, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 10,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("5"),
			DecisionTaskCompletedEventId: 4,
		}),
		createTestEventLocalActivity(6, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("6"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(7, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowLocalActivity)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func testReplayWorkflowGetVersion(ctx Context) error {
	version := GetVersion(ctx, "change_id_A", Version(3), Version(3))
	if version != Version(3) {
		return errors.New("Version mismatch")
	}

	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersion")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowGetVersion)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func testReplayWorkflowGetVersionReplacedChangeID(ctx Context) error {
	version := GetVersion(ctx, "change_id_B", DefaultVersion, Version(1))
	if version != DefaultVersion {
		return errors.New("Version mismatch")
	}

	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion_ReplacedChangeID() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionReplacedChangeID")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionReplacedChangeID)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func testReplayWorkflowGetVersionRemoved(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersionRemoved() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionRemoved")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionRemoved)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func testReplayWorkflowGetVersionAddNewBefore(ctx Context) error {
	version := GetVersion(ctx, "change_id_B", DefaultVersion, Version(1))
	if version != DefaultVersion {
		return errors.New("Unexpected version")
	}

	version = GetVersion(ctx, "change_id_A", Version(3), Version(3))
	if version != Version(3) {
		return errors.New("Version mismatch")
	}

	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion_AddNewBefore() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionAddNewBefore")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionAddNewBefore)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForGetVersionTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventVersionMarker(5, 4, "change_id_A", Version(3)),
		createTestUpsertWorkflowSearchAttributesForChangeVersion(6, 4, "change_id_A", Version(3)),
		createTestEventActivityTaskScheduled(7, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "7",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(8, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 7,
		}),
		createTestEventActivityTaskCompleted(9, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 7,
			StartedEventId:   8,
		}),
		createTestEventDecisionTaskScheduled(10, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(11),
		createTestEventDecisionTaskCompleted(12, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 10,
			StartedEventId:   11,
		}),
		createTestEventActivityTaskScheduled(13, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "13",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(14, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 13,
		}),
		createTestEventActivityTaskCompleted(15, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 13,
			StartedEventId:   14,
		}),
		createTestEventDecisionTaskScheduled(16, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(17),
		createTestEventDecisionTaskCompleted(18, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   17,
		}),
		createTestEventActivityTaskScheduled(19, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "19",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(20, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 19,
		}),
		createTestEventActivityTaskCompleted(21, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 19,
			StartedEventId:   20,
		}),

		createTestEventDecisionTaskScheduled(22, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(23),
		createTestEventDecisionTaskCompleted(24, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 22,
			StartedEventId:   23,
		}),
		createTestEventWorkflowExecutionCompleted(25, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 24,
		}),
	}
}

func testReplayWorkflowCancelActivity(ctx Context) error {
	ctx1, cancelFunc1 := WithCancel(ctx)

	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx1 = WithActivityOptions(ctx1, ao)
	_ = ExecuteActivity(ctx1, "testActivity1")
	_ = Sleep(ctx, 1*time.Second)
	cancelFunc1()

	err := ExecuteActivity(ctx, "testActivity2").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelActivity() {
	testEvents := createHistoryForCancelActivityTests("testReplayWorkflowCancelActivity")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowCancelActivity)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelActivityTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventActivityTaskScheduled(5, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "5",
			ActivityType: &commonpb.ActivityType{Name: "testActivity1"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventTimerStarted(6, 6),
		createTestEventActivityTaskStarted(7, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 5,
		}),
		createTestEventTimerFired(8, 6),
		createTestEventDecisionTaskScheduled(9, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(10),
		createTestEventDecisionTaskCompleted(11, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventActivityTaskCancelRequested(12, &historypb.ActivityTaskCancelRequestedEventAttributes{
			ScheduledEventId:             5,
			DecisionTaskCompletedEventId: 11,
		}),
		createTestEventActivityTaskScheduled(13, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "13",
			ActivityType: &commonpb.ActivityType{Name: "testActivity2"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(14, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 13,
		}),
		createTestEventActivityTaskCompleted(15, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 13,
			StartedEventId:   14,
		}),
		createTestEventDecisionTaskScheduled(16, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(17),
		createTestEventDecisionTaskCompleted(18, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   17,
		}),
		createTestEventWorkflowExecutionCompleted(19, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 18,
		}),
	}
}

func testReplayWorkflowCancelTimer(ctx Context) error {
	ctx1, cancelFunc1 := WithCancel(ctx)

	_ = NewTimer(ctx1, 3*time.Second)
	_ = Sleep(ctx, 1*time.Second)
	cancelFunc1()

	err := ExecuteActivity(ctx, "testActivity2").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelTimer() {
	testEvents := createHistoryForCancelTimerTests("testReplayWorkflowCancelTimer")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowCancelTimer)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelTimerTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventTimerStarted(5, 5),
		createTestEventTimerStarted(6, 6),
		createTestEventTimerFired(7, 6),
		createTestEventDecisionTaskScheduled(8, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(9),
		createTestEventDecisionTaskCompleted(10, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventTimerCanceled(11, 5),
		createTestEventActivityTaskScheduled(12, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "12",
			ActivityType: &commonpb.ActivityType{Name: "testActivity2"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(13, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 12,
		}),
		createTestEventActivityTaskCompleted(14, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 12,
			StartedEventId:   13,
		}),
		createTestEventDecisionTaskScheduled(15, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(16),
		createTestEventDecisionTaskCompleted(17, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 15,
			StartedEventId:   16,
		}),
		createTestEventWorkflowExecutionCompleted(18, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 17,
		}),
	}
}

func testReplayWorkflowCancelChildWorkflow(ctx Context) error {
	childCtx1, cancelFunc1 := WithCancel(ctx)

	opts := ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
		WorkflowID:               "workflowId",
	}
	childCtx1 = WithChildWorkflowOptions(childCtx1, opts)
	_ = ExecuteChildWorkflow(childCtx1, "testWorkflow")
	_ = Sleep(ctx, 1*time.Second)
	cancelFunc1()

	err := ExecuteActivity(ctx, "testActivity2").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", zap.Error(err))
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelChildWorkflow() {
	testEvents := createHistoryForCancelChildWorkflowTests("testReplayWorkflowCancelChildWorkflow")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowCancelChildWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelChildWorkflowTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventStartChildWorkflowExecutionInitiated(5, &historypb.StartChildWorkflowExecutionInitiatedEventAttributes{
			TaskQueue:  &taskqueuepb.TaskQueue{Name: taskQueue},
			WorkflowId: "workflowId",
		}),
		createTestEventTimerStarted(6, 6),
		createTestEventChildWorkflowExecutionStarted(7, &historypb.ChildWorkflowExecutionStartedEventAttributes{
			InitiatedEventId:  5,
			WorkflowType:      &commonpb.WorkflowType{Name: "testWorkflow"},
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventDecisionTaskScheduled(8, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(9),
		createTestEventDecisionTaskCompleted(10, &historypb.DecisionTaskCompletedEventAttributes{}),
		createTestEventTimerFired(11, 6),
		createTestEventDecisionTaskScheduled(12, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(13),
		createTestEventDecisionTaskCompleted(14, &historypb.DecisionTaskCompletedEventAttributes{}),

		createTestEventRequestCancelExternalWorkflowExecutionInitiated(15, &historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes{
			DecisionTaskCompletedEventId: 14,
			WorkflowExecution:            &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventActivityTaskScheduled(16, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "16",
			ActivityType: &commonpb.ActivityType{Name: "testActivity2"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventExternalWorkflowExecutionCancelRequested(17, &historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes{
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
			InitiatedEventId:  15,
		}),
		createTestEventDecisionTaskScheduled(18, &historypb.DecisionTaskScheduledEventAttributes{}),

		createTestEventActivityTaskStarted(19, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 16,
		}),
		createTestEventDecisionTaskStarted(20),
		createTestEventDecisionTaskCompleted(21, &historypb.DecisionTaskCompletedEventAttributes{}),

		createTestEventActivityTaskCompleted(22, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   19,
		}),

		createTestEventChildWorkflowExecutionCanceled(23, &historypb.ChildWorkflowExecutionCanceledEventAttributes{
			InitiatedEventId:  5,
			StartedEventId:    7,
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventDecisionTaskScheduled(24, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(25),
		createTestEventDecisionTaskCompleted(26, &historypb.DecisionTaskCompletedEventAttributes{
			ScheduledEventId: 24,
			StartedEventId:   25,
		}),
		createTestEventWorkflowExecutionCompleted(27, &historypb.WorkflowExecutionCompletedEventAttributes{
			DecisionTaskCompletedEventId: 26,
		}),
	}
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Result_Mismatch() {
	taskQueue := "taskQueue1"
	result, _ := DefaultDataConverter.ToPayloads("some-incorrect-result")
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("5"),
			DecisionTaskCompletedEventId: 4,
		}),
		createTestEventLocalActivity(6, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("6"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(7, &historypb.WorkflowExecutionCompletedEventAttributes{
			Result:                       result,
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowLocalActivity)
	err := replayer.ReplayWorkflowHistory(logger, history)
	if err != nil {
		fmt.Printf("replay failed.  Error: %v", err.Error())
	}
	require.Error(s.T(), err)
	require.True(s.T(), strings.HasPrefix(err.Error(), "replay workflow doesn't return the same result as the last event"))
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Activity_Type_Mismatch() {
	taskQueue := "taskQueue1"
	result, _ := DefaultDataConverter.ToPayloads("some-incorrect-result")
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "go.temporal.io/temporal/internal.testReplayWorkflow"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(getDefaultDataConverter()),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
		createTestEventDecisionTaskCompleted(4, &historypb.DecisionTaskCompletedEventAttributes{}),

		createTestEventLocalActivity(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("0"),
			DecisionTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(6, &historypb.WorkflowExecutionCompletedEventAttributes{
			Result:                       result,
			DecisionTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflow)
	err := replayer.ReplayWorkflowHistory(logger, history)
	require.Error(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFileParent() {
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowFromFileParent)
	err := replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/parentWF.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFile() {
	logger := getLogger()
	replayer := NewWorkflowReplayer()
	replayer.RegisterWorkflow(testReplayWorkflowFromFile)
	err := replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/sampleHistory.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) testDecisionTaskHandlerHelper(params workerExecutionParameters) {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:     testEncodeFunctionArgs(params.DataConverter),
		}),
		createTestEventDecisionTaskScheduled(2, &historypb.DecisionTaskScheduledEventAttributes{}),
		createTestEventDecisionTaskStarted(3),
	}

	workflowType := "testReplayWorkflow"
	workflowID := "testID"
	runID := "testRunID"

	task := &workflowservice.PollForDecisionTaskResponse{
		WorkflowExecution:      &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
		WorkflowType:           &commonpb.WorkflowType{Name: workflowType},
		History:                &historypb.History{Events: testEvents},
		PreviousStartedEventId: 0,
	}

	r := newWorkflowTaskHandler(params, nil, s.registry)
	_, err := r.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	s.NoError(err)
}

func (s *internalWorkerTestSuite) TestDecisionTaskHandlerWithDataConverter() {
	params := workerExecutionParameters{
		Namespace:     testNamespace,
		Identity:      "identity",
		Logger:        getLogger(),
		DataConverter: newTestDataConverter(),
	}
	s.testDecisionTaskHandlerHelper(params)
}

// testSampleWorkflow
func sampleWorkflowExecute(ctx Context, input []byte) (result []byte, err error) {
	ExecuteActivity(ctx, testActivityByteArgs, input)
	ExecuteActivity(ctx, testActivityMultipleArgs, 2, []string{"test"}, true)
	ExecuteActivity(ctx, testActivityMultipleArgsWithStruct, -8, newTestActivityArg())
	return []byte("Done"), nil
}

// test activity1
func testActivityByteArgs(context.Context, []byte) ([]byte, error) {
	fmt.Println("Executing Activity1")
	return nil, nil
}

// test testActivityMultipleArgs
func testActivityMultipleArgs(context.Context, int, []string, bool) ([]byte, error) {
	fmt.Println("Executing Activity2")
	return nil, nil
}

// test testActivityMultipleArgsWithStruct
func testActivityMultipleArgsWithStruct(_ context.Context, i int, s testActivityArg) ([]byte, error) {
	fmt.Printf("Executing testActivityMultipleArgsWithStruct: %d, %v\n", i, s)
	return nil, nil
}

func (s *internalWorkerTestSuite) TestCreateWorker() {
	worker := createWorkerWithThrottle(s.service, 500.0, nil)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
}

func (s *internalWorkerTestSuite) TestCreateWorkerWithDataConverter() {
	worker := createWorkerWithDataConverter(s.service)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	worker.Stop()
}

func (s *internalWorkerTestSuite) TestCreateWorkerRun() {
	// Create service endpoint
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	worker := createWorker(service)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = worker.Run()
	}()
	time.Sleep(time.Millisecond * 200)
	p, err := os.FindProcess(os.Getpid())
	assert.NoError(s.T(), err)
	assert.NoError(s.T(), p.Signal(os.Interrupt))
	wg.Wait()
}

func (s *internalWorkerTestSuite) TestNoActivitiesOrWorkflows() {
	t := s.T()
	w := createWorker(s.service)
	w.registry = newRegistry()
	assert.Empty(t, w.registry.getRegisteredActivities())
	assert.Empty(t, w.registry.getRegisteredWorkflowTypes())
	assert.NoError(t, w.Start())
}

func (s *internalWorkerTestSuite) TestWorkerStartFailsWithInvalidNamespace() {
	t := s.T()
	testCases := []struct {
		namespaceErr error
		isErrFatal   bool
	}{
		{serviceerror.NewNotFound(""), true},
		{serviceerror.NewInvalidArgument(""), true},
		{serviceerror.NewInternal(""), false},
		{errors.New("unknown"), false},
	}

	mockCtrl := gomock.NewController(t)

	for _, tc := range testCases {
		service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
		service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, tc.namespaceErr).Do(
			func(ctx context.Context, request *workflowservice.DescribeNamespaceRequest, opts ...grpc.CallOption) {
				// log
			}).Times(2)

		worker := createWorker(service)
		if tc.isErrFatal {
			err := worker.Start()
			assert.Error(t, err, "worker.start() MUST fail when namespace is invalid")
			errC := make(chan error)
			go func() { errC <- worker.Run() }()
			select {
			case e := <-errC:
				assert.Error(t, e, "worker.Run() MUST fail when namespace is invalid")
			case <-time.After(time.Second):
				assert.Fail(t, "worker.Run() MUST fail when namespace is invalid")
			}
			continue
		}
		err := worker.Start()
		assert.NoError(t, err, "worker.Start() failed unexpectedly")
		worker.Stop()
	}
}

func ofPollForActivityTaskRequest(tps float64) gomock.Matcher {
	return &mockPollForActivityTaskRequest{tps: tps}
}

type mockPollForActivityTaskRequest struct {
	tps float64
}

func (m *mockPollForActivityTaskRequest) Matches(x interface{}) bool {
	v, ok := x.(*workflowservice.PollForActivityTaskRequest)
	if !ok {
		return false
	}

	if v.TaskQueueMetadata != nil && v.TaskQueueMetadata.MaxTasksPerSecond != nil {
		return v.TaskQueueMetadata.MaxTasksPerSecond.GetValue() == m.tps
	}

	return false
}

func (m *mockPollForActivityTaskRequest) String() string {
	return "PollForActivityTaskRequest"
}

func createWorker(service *workflowservicemock.MockWorkflowServiceClient) *AggregatedWorker {
	return createWorkerWithThrottle(service, 0.0, nil)
}

func createWorkerWithThrottle(
	service *workflowservicemock.MockWorkflowServiceClient, activitiesPerSecond float64, dc DataConverter,
) *AggregatedWorker {
	namespace := "testNamespace"
	namespaceStatus := enumspb.NAMESPACE_STATUS_REGISTERED
	namespaceDesc := &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespacepb.NamespaceInfo{
			Name:   namespace,
			Status: namespaceStatus,
		},
	}
	// mocks
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(namespaceDesc, nil).Do(
		func(ctx context.Context, request *workflowservice.DescribeNamespaceRequest, opts ...grpc.CallOption) {
			// log
		}).AnyTimes()

	activityTask := &workflowservice.PollForActivityTaskResponse{}
	expectedActivitiesPerSecond := activitiesPerSecond
	if expectedActivitiesPerSecond == 0.0 {
		expectedActivitiesPerSecond = defaultTaskQueueActivitiesPerSecond
	}
	service.EXPECT().PollForActivityTask(
		gomock.Any(), ofPollForActivityTaskRequest(expectedActivitiesPerSecond), gomock.Any(),
	).Return(activityTask, nil).AnyTimes()
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	decisionTask := &workflowservice.PollForDecisionTaskResponse{}
	service.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(decisionTask, nil).AnyTimes()
	service.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	// Configure worker options.
	workerOptions := WorkerOptions{
		WorkerActivitiesPerSecond:    20,
		TaskQueueActivitiesPerSecond: activitiesPerSecond,
		EnableSessionWorker:          true}

	clientOptions := ClientOptions{
		Namespace: namespace,
	}
	if dc != nil {
		clientOptions.DataConverter = dc
	}

	client := NewServiceClient(service, nil, clientOptions)
	worker := NewAggregatedWorker(client, "testGroupName2", workerOptions)
	return worker
}

func createWorkerWithDataConverter(service *workflowservicemock.MockWorkflowServiceClient) *AggregatedWorker {
	return createWorkerWithThrottle(service, 0.0, newTestDataConverter())
}

func (s *internalWorkerTestSuite) testCompleteActivityHelper(opt ClientOptions) {
	t := s.T()
	mockService := s.service
	wfClient := NewServiceClient(mockService, nil, opt)
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedRequest, opts ...grpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceled(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCanceledResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledRequest, opts ...grpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskFailedResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskFailedRequest, opts ...grpc.CallOption) {
			failedRequest = request
		})

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, nil)
	require.NotNil(t, completedRequest)

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	_ = wfClient.CompleteActivity(context.Background(), []byte("task-token"), nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func (s *internalWorkerTestSuite) TestCompleteActivity() {
	s.testCompleteActivityHelper(ClientOptions{})
}

func (s *internalWorkerTestSuite) TestCompleteActivityWithDataConverter() {
	opt := ClientOptions{DataConverter: newTestDataConverter()}
	s.testCompleteActivityHelper(opt)
}

func (s *internalWorkerTestSuite) TestCompleteActivityById() {
	t := s.T()
	mockService := s.service
	wfClient := NewServiceClient(mockService, nil, ClientOptions{Namespace: "testNamespace"})
	var completedRequest, canceledRequest, failedRequest interface{}
	mockService.EXPECT().RespondActivityTaskCompletedById(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedByIdResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCompletedByIdRequest, opts ...grpc.CallOption) {
			completedRequest = request
		})
	mockService.EXPECT().RespondActivityTaskCanceledById(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCanceledByIdResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskCanceledByIdRequest, opts ...grpc.CallOption) {
			canceledRequest = request
		})
	mockService.EXPECT().RespondActivityTaskFailedById(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskFailedByIdResponse{}, nil).Do(
		func(ctx context.Context, request *workflowservice.RespondActivityTaskFailedByIdRequest, opts ...grpc.CallOption) {
			failedRequest = request
		})

	workflowID := "wid"
	runID := ""
	activityID := "aid"

	_ = wfClient.CompleteActivityByID(context.Background(), DefaultNamespace, workflowID, runID, activityID, nil, nil)
	require.NotNil(t, completedRequest)

	_ = wfClient.CompleteActivityByID(context.Background(), DefaultNamespace, workflowID, runID, activityID, nil, NewCanceledError())
	require.NotNil(t, canceledRequest)

	_ = wfClient.CompleteActivityByID(context.Background(), DefaultNamespace, workflowID, runID, activityID, nil, errors.New(""))
	require.NotNil(t, failedRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeat() {
	wfClient := NewServiceClient(s.service, nil, ClientOptions{Namespace: "testNamespace"})
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
		}).Times(2)

	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil)
	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil, "testStack", "customerObjects", 4)
	require.NotNil(s.T(), heartbeatRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeatWithDataConverter() {
	t := s.T()
	dc := newTestDataConverter()
	opt := ClientOptions{Namespace: "testNamespace", DataConverter: dc}
	wfClient := NewServiceClient(s.service, nil, opt)
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: false}
	detail1 := "testStack"
	detail2 := testStruct{"abc", 123}
	detail3 := 4
	encodedDetail, err := dc.ToPayloads(detail1, detail2, detail3)
	require.Nil(t, err)
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
			require.Equal(t, encodedDetail, request.Details)
		}).Times(1)

	_ = wfClient.RecordActivityHeartbeat(context.Background(), nil, detail1, detail2, detail3)
	require.NotNil(t, heartbeatRequest)
}

func (s *internalWorkerTestSuite) TestRecordActivityHeartbeatByID() {
	wfClient := NewServiceClient(s.service, nil, ClientOptions{Namespace: "testNamespace"})
	var heartbeatRequest *workflowservice.RecordActivityTaskHeartbeatByIdRequest
	heartbeatResponse := workflowservice.RecordActivityTaskHeartbeatByIdResponse{CancelRequested: false}
	s.service.EXPECT().RecordActivityTaskHeartbeatById(gomock.Any(), gomock.Any(), gomock.Any()).Return(&heartbeatResponse, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatByIdRequest, opts ...grpc.CallOption) {
			heartbeatRequest = request
		}).Times(2)

	_ = wfClient.RecordActivityHeartbeatByID(context.Background(), DefaultNamespace, "wid", "rid", "aid")
	_ = wfClient.RecordActivityHeartbeatByID(context.Background(), DefaultNamespace, "wid", "rid", "aid",
		"testStack", "customerObjects", 4)
	require.NotNil(s.T(), heartbeatRequest)
}

type activitiesCallingOptionsWorkflow struct {
	t *testing.T
}

func (w activitiesCallingOptionsWorkflow) Execute(ctx Context, input []byte) (result []byte, err error) {
	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)

	// By functions.
	err = ExecuteActivity(ctx, testActivityByteArgs, input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityMultipleArgs, 2, []string{"test"}, true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityMultipleArgsWithStruct, -8, newTestActivityArg()).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoResult, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, testActivityNoContextArg, 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f := ExecuteActivity(ctx, testActivityReturnByteArray)
	var r []byte
	err = f.Get(ctx, &r)
	require.NoError(w.t, err, err)
	require.Equal(w.t, []byte("testActivity"), r)

	f = ExecuteActivity(ctx, testActivityReturnInt)
	var rInt int
	err = f.Get(ctx, &rInt)
	require.NoError(w.t, err, err)
	require.Equal(w.t, 5, rInt)

	f = ExecuteActivity(ctx, testActivityReturnString)
	var rString string
	err = f.Get(ctx, &rString)

	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString)

	f = ExecuteActivity(ctx, testActivityReturnEmptyString)
	var r2String string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2String)

	f = ExecuteActivity(ctx, testActivityReturnEmptyStruct)
	var r2Struct testActivityResult
	err = f.Get(ctx, &r2Struct)
	require.NoError(w.t, err, err)
	require.Equal(w.t, testActivityResult{}, r2Struct)

	f = ExecuteActivity(ctx, testActivityReturnNilStructPtr)
	var rStructPtr *testActivityResult
	err = f.Get(ctx, &rStructPtr)
	require.NoError(w.t, err, err)
	require.True(w.t, rStructPtr == nil)

	f = ExecuteActivity(ctx, testActivityReturnStructPtr)
	err = f.Get(ctx, &rStructPtr)
	require.NoError(w.t, err, err)
	require.Equal(w.t, *rStructPtr, testActivityResult{Index: 10})

	f = ExecuteActivity(ctx, testActivityReturnNilStructPtrPtr)
	var rStruct2Ptr **testActivityResult
	err = f.Get(ctx, &rStruct2Ptr)
	require.NoError(w.t, err, err)
	require.True(w.t, rStruct2Ptr == nil)

	f = ExecuteActivity(ctx, testActivityReturnStructPtrPtr)
	err = f.Get(ctx, &rStruct2Ptr)
	require.NoError(w.t, err, err)
	require.True(w.t, **rStruct2Ptr == testActivityResult{Index: 10})

	// By names.
	err = ExecuteActivity(ctx, "testActivityByteArgs", input).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityMultipleArgs", 2, []string{"test"}, true).Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoResult", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	err = ExecuteActivity(ctx, "testActivityNoContextArg", 2, "test").Get(ctx, nil)
	require.NoError(w.t, err, err)

	f = ExecuteActivity(ctx, "testActivityReturnString")
	err = f.Get(ctx, &rString)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "testActivity", rString, rString)

	f = ExecuteActivity(ctx, "testActivityReturnEmptyString")
	var r2sString string
	err = f.Get(ctx, &r2String)
	require.NoError(w.t, err, err)
	require.Equal(w.t, "", r2sString)

	f = ExecuteActivity(ctx, "testActivityReturnEmptyStruct")
	err = f.Get(ctx, &r2Struct)
	require.NoError(w.t, err, err)
	require.Equal(w.t, testActivityResult{}, r2Struct)

	return []byte("Done"), nil
}

// test testActivityNoResult
func testActivityNoResult(context.Context, int, string) error {
	return nil
}

// test testActivityNoContextArg
func testActivityNoContextArg(int, string) error {
	return nil
}

// test testActivityReturnByteArray
func testActivityReturnByteArray() ([]byte, error) {
	return []byte("testActivity"), nil
}

// testActivityReturnInt
func testActivityReturnInt() (int, error) {
	return 5, nil
}

// testActivityReturnString
func testActivityReturnString() (string, error) {
	return "testActivity", nil
}

// testActivityReturnEmptyString
func testActivityReturnEmptyString() (string, error) {
	// Return is mocked to retrun nil from server.
	// expect to convert it to appropriate default value.
	return "", nil
}

type testActivityArg struct {
	Index    int
	Name     string
	Data     []byte
	IndexPtr *int
	NamePtr  *string
	DataPtr  *[]byte
}

type testActivityResult struct {
	Index int
}

func newTestActivityArg() *testActivityArg {
	name := "JohnSmith"
	index := 22
	data := []byte{22, 8, 78}

	return &testActivityArg{
		Name:     name,
		Index:    index,
		Data:     data,
		NamePtr:  &name,
		IndexPtr: &index,
		DataPtr:  &data,
	}
}

// testActivityReturnEmptyStruct
func testActivityReturnEmptyStruct() (testActivityResult, error) {
	// Return is mocked to retrun nil from server.
	// expect to convert it to appropriate default value.
	return testActivityResult{}, nil
}
func testActivityReturnNilStructPtr() (*testActivityResult, error) {
	return nil, nil
}
func testActivityReturnStructPtr() (*testActivityResult, error) {
	return &testActivityResult{Index: 10}, nil
}
func testActivityReturnNilStructPtrPtr() (**testActivityResult, error) {
	return nil, nil
}
func testActivityReturnStructPtrPtr() (**testActivityResult, error) {
	r := &testActivityResult{Index: 10}
	return &r, nil
}

func TestVariousActivitySchedulingOption(t *testing.T) {
	w := &activitiesCallingOptionsWorkflow{t: t}

	testVariousActivitySchedulingOption(t, w.Execute)
	testVariousActivitySchedulingOptionWithDataConverter(t, w.Execute)
}

func testVariousActivitySchedulingOption(t *testing.T, wf interface{}) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(wf)
	testInternalWorkerRegisterWithTestEnv(env)
	env.ExecuteWorkflow(wf, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func testVariousActivitySchedulingOptionWithDataConverter(t *testing.T, wf interface{}) {
	ts := &WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	env.SetDataConverter(newTestDataConverter())
	env.RegisterWorkflow(wf)
	testInternalWorkerRegisterWithTestEnv(env)
	env.ExecuteWorkflow(wf, []byte{1, 2})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func testWorkflowSample(Context, []byte) (result []byte, err error) {
	return nil, nil
}

func testWorkflowMultipleArgs(Context, int, string, bool) (result []byte, err error) {
	return nil, nil
}

func testWorkflowNoArgs(Context) (result []byte, err error) {
	return nil, nil
}

func testWorkflowReturnInt(Context) (result int, err error) {
	return 5, nil
}

func testWorkflowReturnString(Context, int) (result string, err error) {
	return "Done", nil
}

type testWorkflowResult struct {
	V int
}

func testWorkflowReturnStruct(Context, int) (result testWorkflowResult, err error) {
	return testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtr(Context, int) (result *testWorkflowResult, err error) {
	return &testWorkflowResult{}, nil
}

func testWorkflowReturnStructPtrPtr(Context, int) (result **testWorkflowResult, err error) {
	return nil, nil
}

func TestRegisterVariousWorkflowTypes(t *testing.T) {
	r := newRegistry()
	r.RegisterWorkflow(testWorkflowSample)
	r.RegisterWorkflow(testWorkflowMultipleArgs)
	r.RegisterWorkflow(testWorkflowNoArgs)
	r.RegisterWorkflow(testWorkflowReturnInt)
	r.RegisterWorkflow(testWorkflowReturnString)
	r.RegisterWorkflow(testWorkflowReturnStruct)
	r.RegisterWorkflow(testWorkflowReturnStructPtr)
	r.RegisterWorkflow(testWorkflowReturnStructPtrPtr)
}

type testErrorDetails struct {
	T string
}

func testActivityErrorWithDetailsHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewApplicationError("testReason", false, nil, "testStringDetails")
		}}
	_, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	require.Error(t, e)
	errWD := e.(*ApplicationError)
	require.Equal(t, "testReason", errWD.Error())
	var strDetails string
	_ = errWD.Details(&strDetails)
	require.Equal(t, "testStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewApplicationError("testReason", false, nil, testErrorDetails{T: "testErrorStack"})
		}}
	_, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	require.Error(t, e)
	errWD = e.(*ApplicationError)
	require.Equal(t, "testReason", errWD.Error())
	var td testErrorDetails
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewApplicationError("testReason", false, nil, testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e := a3.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	var result string
	err := dataConverter.FromPayloads(encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult", result)
	require.Error(t, e)
	errWD = e.(*ApplicationError)
	require.Equal(t, "testReason", errWD.Error())
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewApplicationError("testReason", false, nil, "testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = dataConverter.FromPayloads(encResult, &result)
	require.NoError(t, err)
	require.Equal(t, "testResult4", result)
	require.Error(t, e)
	errWD = e.(*ApplicationError)
	require.Equal(t, "testReason", errWD.Error())
	var ed string
	_ = errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityErrorWithDetailsWithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{dataConverter: dc})
	testActivityErrorWithDetailsHelper(ctx, t, dc)
}

func testActivityCancelledErrorHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError("testCancelStringDetails")
		}}
	_, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	require.Error(t, e)
	errWD := e.(*CanceledError)
	var strDetails string
	_ = errWD.Details(&strDetails)
	require.Equal(t, "testCancelStringDetails", strDetails)

	a2 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewCanceledError(testErrorDetails{T: "testCancelErrorStack"})
		}}
	_, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var td testErrorDetails
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testCancelErrorStack"}, td)

	a3 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult", NewCanceledError(testErrorDetails{T: "testErrorStack3"})
		}}
	encResult, e := a3.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	var r string
	err := dataConverter.FromPayloads(encResult, &r)
	require.NoError(t, err)
	require.Equal(t, "testResult", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	_ = errWD.Details(&td)
	require.Equal(t, testErrorDetails{T: "testErrorStack3"}, td)

	a4 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (result string, err error) {
			return "testResult4", NewCanceledError("testMultipleString", testErrorDetails{T: "testErrorStack4"})
		}}
	encResult, e = a4.Execute(ctx, testEncodeFunctionArgs(dataConverter, 1))
	err = dataConverter.FromPayloads(encResult, &r)
	require.NoError(t, err)
	require.Equal(t, "testResult4", r)
	require.Error(t, e)
	errWD = e.(*CanceledError)
	var ed string
	_ = errWD.Details(&ed, &td)
	require.Equal(t, "testMultipleString", ed)
	require.Equal(t, testErrorDetails{T: "testErrorStack4"}, td)
}

func TestActivityCancelledErrorWithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{dataConverter: dc})
	testActivityCancelledErrorHelper(ctx, t, dc)
}

func testActivityExecutionVariousTypesHelper(ctx context.Context, t *testing.T, dataConverter DataConverter) {
	a1 := activityExecutor{
		fn: func(ctx context.Context, arg1 string) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 1}, nil
		}}
	encResult, e := a1.Execute(ctx, testEncodeFunctionArgs(dataConverter, "test"))
	require.NoError(t, e)
	var r *testWorkflowResult
	err := dataConverter.FromPayloads(encResult, &r)
	require.NoError(t, err)
	require.Equal(t, 1, r.V)

	a2 := activityExecutor{
		fn: func(ctx context.Context, arg1 *testWorkflowResult) (*testWorkflowResult, error) {
			return &testWorkflowResult{V: 2}, nil
		}}
	encResult, e = a2.Execute(ctx, testEncodeFunctionArgs(dataConverter, r))
	require.NoError(t, e)
	err = dataConverter.FromPayloads(encResult, &r)
	require.NoError(t, err)
	require.Equal(t, 2, r.V)
}

func TestActivityExecutionVariousTypesWithDataConverter(t *testing.T) {
	dc := newTestDataConverter()
	ctx := context.WithValue(context.Background(), activityEnvContextKey, &activityEnvironment{
		dataConverter: dc,
	})
	testActivityExecutionVariousTypesHelper(ctx, t, dc)
}

func TestActivityNilArgs(t *testing.T) {
	nilErr := errors.New("nils")
	activityFn := func(name string, idx int, strptr *string) error {
		if name == "" && idx == 0 && strptr == nil {
			return nilErr
		}
		return nil
	}

	args := []interface{}{nil, nil, nil}
	_, err := getValidatedActivityFunction(activityFn, args, newRegistry())
	require.NoError(t, err)

	dataConverter := getDefaultDataConverter()
	data, _ := encodeArgs(dataConverter, args)
	reflectArgs, err := decodeArgs(dataConverter, reflect.TypeOf(activityFn), data)
	require.NoError(t, err)

	reflectResults := reflect.ValueOf(activityFn).Call(reflectArgs)
	require.Equal(t, nilErr, reflectResults[0].Interface())
}

func TestWorkerOptionDefaults(t *testing.T) {
	client := &WorkflowClient{}
	taskQueue := "worker-options-tq"
	aggWorker := NewAggregatedWorker(client, taskQueue, WorkerOptions{})

	decisionWorker := aggWorker.workflowWorker
	require.True(t, decisionWorker.executionParameters.Identity != "")
	require.NotNil(t, decisionWorker.executionParameters.Logger)
	require.NotNil(t, decisionWorker.executionParameters.MetricsScope)
	require.Nil(t, decisionWorker.executionParameters.ContextPropagators)

	expected := workerExecutionParameters{
		Namespace:                            DefaultNamespace,
		TaskQueue:                            taskQueue,
		MaxConcurrentActivityPollers:         defaultConcurrentPollRoutineSize,
		MaxConcurrentDecisionPollers:         defaultConcurrentPollRoutineSize,
		ConcurrentLocalActivityExecutionSize: defaultMaxConcurrentLocalActivityExecutionSize,
		ConcurrentActivityExecutionSize:      defaultMaxConcurrentActivityExecutionSize,
		ConcurrentDecisionTaskExecutionSize:  defaultMaxConcurrentTaskExecutionSize,
		WorkerActivitiesPerSecond:            defaultTaskQueueActivitiesPerSecond,
		WorkerDecisionTasksPerSecond:         defaultWorkerTaskExecutionRate,
		TaskQueueActivitiesPerSecond:         defaultTaskQueueActivitiesPerSecond,
		WorkerLocalActivitiesPerSecond:       defaultWorkerLocalActivitiesPerSecond,
		StickyScheduleToStartTimeout:         stickyDecisionScheduleToStartTimeoutSeconds * time.Second,
		DataConverter:                        getDefaultDataConverter(),
		Tracer:                               opentracing.NoopTracer{},
		Logger:                               decisionWorker.executionParameters.Logger,
		MetricsScope:                         decisionWorker.executionParameters.MetricsScope,
		Identity:                             decisionWorker.executionParameters.Identity,
		UserContext:                          decisionWorker.executionParameters.UserContext,
	}

	assertWorkerExecutionParamsEqual(t, expected, decisionWorker.executionParameters)

	activityWorker := aggWorker.activityWorker
	require.True(t, activityWorker.executionParameters.Identity != "")
	require.NotNil(t, activityWorker.executionParameters.Logger)
	require.NotNil(t, activityWorker.executionParameters.MetricsScope)
	require.Nil(t, activityWorker.executionParameters.ContextPropagators)
	assertWorkerExecutionParamsEqual(t, expected, activityWorker.executionParameters)
}

func TestWorkerOptionNonDefaults(t *testing.T) {
	taskQueue := "worker-options-tq"

	client := &WorkflowClient{
		workflowService:    nil,
		connectionCloser:   nil,
		namespace:          "worker-options-test",
		registry:           nil,
		identity:           "143@worker-options-test-1",
		dataConverter:      &defaultDataConverter{},
		contextPropagators: nil,
		tracer:             nil,
		logger:             zap.NewNop(),
	}

	options := WorkerOptions{
		TaskQueueActivitiesPerSecond:            8888,
		MaxConcurrentSessionExecutionSize:       3333,
		MaxConcurrentDecisionTaskExecutionSize:  2222,
		MaxConcurrentActivityExecutionSize:      1111,
		MaxConcurrentLocalActivityExecutionSize: 101,
		MaxConcurrentDecisionTaskPollers:        11,
		MaxConcurrentActivityTaskPollers:        12,
		WorkerLocalActivitiesPerSecond:          222,
		WorkerDecisionTasksPerSecond:            111,
		WorkerActivitiesPerSecond:               99,
		StickyScheduleToStartTimeout:            555 * time.Minute,
		BackgroundActivityContext:               context.Background(),
	}

	aggWorker := NewAggregatedWorker(client, taskQueue, options)

	decisionWorker := aggWorker.workflowWorker
	require.Len(t, decisionWorker.executionParameters.ContextPropagators, 0)

	expected := workerExecutionParameters{
		TaskQueue:                            taskQueue,
		MaxConcurrentActivityPollers:         options.MaxConcurrentActivityTaskPollers,
		MaxConcurrentDecisionPollers:         options.MaxConcurrentDecisionTaskPollers,
		ConcurrentLocalActivityExecutionSize: options.MaxConcurrentLocalActivityExecutionSize,
		ConcurrentActivityExecutionSize:      options.MaxConcurrentActivityExecutionSize,
		ConcurrentDecisionTaskExecutionSize:  options.MaxConcurrentDecisionTaskExecutionSize,
		WorkerActivitiesPerSecond:            options.WorkerActivitiesPerSecond,
		WorkerDecisionTasksPerSecond:         options.WorkerDecisionTasksPerSecond,
		TaskQueueActivitiesPerSecond:         options.TaskQueueActivitiesPerSecond,
		WorkerLocalActivitiesPerSecond:       options.WorkerLocalActivitiesPerSecond,
		StickyScheduleToStartTimeout:         options.StickyScheduleToStartTimeout,
		DataConverter:                        client.dataConverter,
		Tracer:                               client.tracer,
		Logger:                               client.logger,
		MetricsScope:                         client.metricsScope,
		Identity:                             client.identity,
	}

	assertWorkerExecutionParamsEqual(t, expected, decisionWorker.executionParameters)

	activityWorker := aggWorker.activityWorker
	require.Len(t, activityWorker.executionParameters.ContextPropagators, 0)
	assertWorkerExecutionParamsEqual(t, expected, activityWorker.executionParameters)
}

func assertWorkerExecutionParamsEqual(t *testing.T, paramsA workerExecutionParameters, paramsB workerExecutionParameters) {
	require.Equal(t, paramsA.TaskQueue, paramsA.TaskQueue)
	require.Equal(t, paramsA.Identity, paramsB.Identity)
	require.Equal(t, paramsA.DataConverter, paramsB.DataConverter)
	require.Equal(t, paramsA.Tracer, paramsB.Tracer)
	require.Equal(t, paramsA.ConcurrentLocalActivityExecutionSize, paramsB.ConcurrentLocalActivityExecutionSize)
	require.Equal(t, paramsA.ConcurrentActivityExecutionSize, paramsB.ConcurrentActivityExecutionSize)
	require.Equal(t, paramsA.ConcurrentDecisionTaskExecutionSize, paramsB.ConcurrentDecisionTaskExecutionSize)
	require.Equal(t, paramsA.WorkerActivitiesPerSecond, paramsB.WorkerActivitiesPerSecond)
	require.Equal(t, paramsA.WorkerDecisionTasksPerSecond, paramsB.WorkerDecisionTasksPerSecond)
	require.Equal(t, paramsA.TaskQueueActivitiesPerSecond, paramsB.TaskQueueActivitiesPerSecond)
	require.Equal(t, paramsA.StickyScheduleToStartTimeout, paramsB.StickyScheduleToStartTimeout)
	require.Equal(t, paramsA.MaxConcurrentDecisionPollers, paramsB.MaxConcurrentDecisionPollers)
	require.Equal(t, paramsA.MaxConcurrentActivityPollers, paramsB.MaxConcurrentActivityPollers)
	require.Equal(t, paramsA.WorkflowPanicPolicy, paramsB.WorkflowPanicPolicy)
	require.Equal(t, paramsA.EnableLoggingInReplay, paramsB.EnableLoggingInReplay)
	require.Equal(t, paramsA.DisableStickyExecution, paramsB.DisableStickyExecution)
}

/*
type encodingTest struct {
	encoding encoding
	input    []interface{}
}

var testWorkflowID1 = s.WorkflowExecution{WorkflowId: common.StringPtr("testWID"), RunId: common.StringPtr("runID")}
var testWorkflowID2 = s.WorkflowExecution{WorkflowId: common.StringPtr("testWID2"), RunId: common.StringPtr("runID2")}
var thriftEncodingTests = []encodingTest{
	{&thriftEncoding{}, []interface{}{&testWorkflowID1}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2}},
	{&thriftEncoding{}, []interface{}{&testWorkflowID1, &testWorkflowID2, &testWorkflowID1}},
}

// TODO: Disable until thriftrw encoding support is added to temporal client.(follow up change)
func _TestThriftEncoding(t *testing.T) {
	// Success tests.
	for _, et := range thriftEncodingTests {
		data, err := et.encoding.Marshal(et.input)
		require.NoError(t, err)

		var result []interface{}
		for _, v := range et.input {
			arg := reflect.New(reflect.ValueOf(v).Type()).Interface()
			result = append(result, arg)
		}
		err = et.encoding.Unmarshal(data, result)
		require.NoError(t, err)

		for i := 0; i < len(et.input); i++ {
			vat := reflect.ValueOf(result[i]).Elem().Interface()
			require.Equal(t, et.input[i], vat)
		}
	}

	// Failure tests.
	enc := &thriftEncoding{}
	_, err := enc.Marshal([]interface{}{testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{&testWorkflowID1})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")

	_, err = enc.Marshal([]interface{}{testWorkflowID1, &testWorkflowID2})
	require.Contains(t, err.Error(), "pointer to thrift.TStruct type is required")

	err = enc.Unmarshal([]byte("dummy"), []interface{}{testWorkflowID1, &testWorkflowID2})
	require.Contains(t, err.Error(), "pointer to pointer thrift.TStruct type is required")
}
*/

// Encode function args
func testEncodeFunctionArgs(dataConverter DataConverter, args ...interface{}) *commonpb.Payloads {
	input, err := encodeArgs(dataConverter, args)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode arguments")
	}
	return input
}
