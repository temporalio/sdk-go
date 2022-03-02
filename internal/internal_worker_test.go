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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/converter"
	iconverter "go.temporal.io/sdk/internal/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
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

	r.RegisterActivityWithOptions(&testActivityStructWithFns{}, RegisterActivityOptions{
		Name:                       "testActivityStructWithFns_",
		SkipInvalidStructFunctions: true,
	})
	r.RegisterActivity(&testActivityStruct{})
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

	env.RegisterActivityWithOptions(&testActivityStructWithFns{}, RegisterActivityOptions{
		Name:                       "testActivityStructWithFns_",
		SkipInvalidStructFunctions: true,
	})
	env.RegisterActivity(&testActivityStruct{})
}

type internalWorkerTestSuite struct {
	suite.Suite
	mockCtrl      *gomock.Controller
	service       *workflowservicemock.MockWorkflowServiceClient
	registry      *registry
	dataConverter converter.DataConverter
}

func TestInternalWorkerTestSuite(t *testing.T) {
	s := &internalWorkerTestSuite{
		registry:      newRegistry(),
		dataConverter: converter.GetDefaultDataConverter(),
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

	result, err := s.dataConverter.ToPayloads(nil)
	s.NoError(err)

	return map[string]*commonpb.Payloads{
		localActivityMarkerDataName: markerData,
		localActivityResultName:     result,
	}
}

func (s *internalWorkerTestSuite) createSideEffectMarkerDataForTest(payloads *commonpb.Payloads,
	sideEffectID int64) map[string]*commonpb.Payloads {
	idPayload, err := s.dataConverter.ToPayloads(sideEffectID)
	s.NoError(err)
	return map[string]*commonpb.Payloads{
		sideEffectMarkerDataName: payloads,
		sideEffectMarkerIDName:   idPayload,
	}
}

func getLogger() log.Logger {
	return ilog.NewDefaultLogger()
}

func testReplayWorkflow(ctx Context) error {
	ao := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	ctx = WithActivityOptions(ctx, ao)
	err := ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteLocalActivity(ctx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
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
		getLogger().Error("activity failed with error.", tagError, err)
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
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
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
		createTestEventWorkflowTaskScheduled(8, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(9),
		createTestEventWorkflowTaskCompleted(10, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 8,
			StartedEventId:   9,
		}),
		createTestEventWorkflowExecutionCompleted(11, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 10,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflow)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventMarkerRecorded(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("1"),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventMarkerRecorded(6, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("2"),
			WorkflowTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(7, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowLocalActivity)
	err = replayer.ReplayWorkflowHistory(logger, history)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersion")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowGetVersion)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func testReplayWorkflowLocalAndRemoteActivity(ctx Context) error {
	version := GetVersion(ctx, "change_id_A", Version(3), Version(3))
	if version != Version(3) {
		return errors.New("Version mismatch")
	}

	localOptions := LocalActivityOptions{
		ScheduleToCloseTimeout: time.Second,
	}
	localCtx := WithLocalActivityOptions(ctx, localOptions)

	err := ExecuteLocalActivity(localCtx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteLocalActivity(localCtx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}

	remoteOptions := ActivityOptions{
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Second,
	}
	remoteCtx := WithActivityOptions(ctx, remoteOptions)

	actF := ExecuteActivity(remoteCtx, "testActivity")

	err = ExecuteLocalActivity(localCtx, testActivity).Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}

	err = actF.Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}

	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalAndRemoteActivity() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowLocalAndRemoteActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventVersionMarker(5, 4, "change_id_A", Version(3)),
		createTestUpsertWorkflowSearchAttributesForChangeVersion(6, 4, "change_id_A", Version(3)),

		createTestEventMarkerRecorded(7, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("1"),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventMarkerRecorded(8, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("2"),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventActivityTaskScheduled(9, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "9",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventMarkerRecorded(10, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("3"),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventActivityTaskStarted(11, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 9,
		}),
		createTestEventActivityTaskCompleted(12, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 9,
			StartedEventId:   11,
		}),
		createTestEventWorkflowTaskScheduled(13, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(14),
		createTestEventWorkflowTaskCompleted(15, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 13,
			StartedEventId:   14,
		}),

		createTestEventWorkflowExecutionCompleted(16, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowLocalAndRemoteActivity)
	err = replayer.ReplayWorkflowHistory(logger, history)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion_ReplacedChangeID() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionReplacedChangeID")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionReplacedChangeID)
	err = replayer.ReplayWorkflowHistory(logger, history)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersionRemoved() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionRemoved")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionRemoved)
	err = replayer.ReplayWorkflowHistory(logger, history)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	err = ExecuteActivity(ctx, "testActivity").Get(ctx, nil)
	if err != nil {
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersion_AddNewBefore() {
	testEvents := createHistoryForGetVersionTests("testReplayWorkflowGetVersionAddNewBefore")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionAddNewBefore)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForGetVersionTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
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
		createTestEventWorkflowTaskScheduled(10, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(11),
		createTestEventWorkflowTaskCompleted(12, &historypb.WorkflowTaskCompletedEventAttributes{
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
		createTestEventWorkflowTaskScheduled(16, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(17),
		createTestEventWorkflowTaskCompleted(18, &historypb.WorkflowTaskCompletedEventAttributes{
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

		createTestEventWorkflowTaskScheduled(22, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(23),
		createTestEventWorkflowTaskCompleted(24, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 22,
			StartedEventId:   23,
		}),
		createTestEventWorkflowExecutionCompleted(25, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 24,
		}),
	}
}

func testReplayWorkflowGetVersionWithSideEffect(ctx Context) error {
	var uniqueID *string

	v := GetVersion(ctx, "UniqueID", DefaultVersion, 1)
	if v == 1 {
		encodedUID := SideEffect(ctx, func(ctx Context) interface{} {
			return "TEST-UNIQUE-ID"
		})
		err := encodedUID.Get(&uniqueID)
		if err != nil {
			return err
		}
	}

	var result string
	err := ExecuteActivity(ctx, "testActivityReturnString").Get(ctx, &result)
	if err != nil {
		return err
	}

	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_GetVersionWithSideEffect() {
	taskQueue := "taskQueue1"
	sideEffectPayloads, seErr := s.dataConverter.ToPayloads("TEST-UNIQUE-ID")
	s.NoError(seErr)
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowGetVersionWithSideEffect"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventVersionMarker(5, 4, "UniqueID", Version(1)),
		createTestUpsertWorkflowSearchAttributesForChangeVersion(6, 4, "UniqueID", Version(1)),
		createTestEventMarkerRecorded(7, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   sideEffectMarkerName,
			Details:                      s.createSideEffectMarkerDataForTest(sideEffectPayloads, 1),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventActivityTaskScheduled(8, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "8",
			ActivityType: &commonpb.ActivityType{Name: "testActivityReturnString"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(9, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 8,
		}),
		createTestEventActivityTaskCompleted(10, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 8,
			StartedEventId:   9,
		}),
		createTestEventWorkflowTaskScheduled(11, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(12),
		createTestEventWorkflowTaskCompleted(13, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 11,
			StartedEventId:   12,
		}),
		createTestEventWorkflowExecutionCompleted(14, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 13,
		}),
	}
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowGetVersionWithSideEffect)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelActivity() {
	testEvents := createHistoryForCancelActivityTests("testReplayWorkflowCancelActivity")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowCancelActivity)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelActivityTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
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
		createTestEventWorkflowTaskScheduled(9, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(10),
		createTestEventWorkflowTaskCompleted(11, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventActivityTaskCancelRequested(12, &historypb.ActivityTaskCancelRequestedEventAttributes{
			ScheduledEventId:             5,
			WorkflowTaskCompletedEventId: 11,
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
		createTestEventWorkflowTaskScheduled(16, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(17),
		createTestEventWorkflowTaskCompleted(18, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   17,
		}),
		createTestEventWorkflowExecutionCompleted(19, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 18,
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelTimer() {
	testEvents := createHistoryForCancelTimerTests("testReplayWorkflowCancelTimer")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowCancelTimer)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelTimerTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerStarted(5, 5),
		createTestEventTimerStarted(6, 6),
		createTestEventTimerFired(7, 6),
		createTestEventWorkflowTaskScheduled(8, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(9),
		createTestEventWorkflowTaskCompleted(10, &historypb.WorkflowTaskCompletedEventAttributes{}),
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
		createTestEventWorkflowTaskScheduled(15, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(16),
		createTestEventWorkflowTaskCompleted(17, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 15,
			StartedEventId:   16,
		}),
		createTestEventWorkflowExecutionCompleted(18, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 17,
		}),
	}
}

func cancelTimerAfterActivityWorkflow(ctx Context) error {
	timerCtx1, cancelFunc1 := WithCancel(ctx)
	_ = NewTimer(timerCtx1, 3*time.Second)
	// Start an activity
	fut := ExecuteActivity(ctx, "testActivity")
	// Cancel timer
	cancelFunc1()
	// Then wait on the activity
	err := fut.Get(ctx, nil)
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowCancelTimerAfterActivity() {
	testEvents := createHistoryForCancelTimerAfterActivity("cancelTimerAfterActivityWorkflow")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(cancelTimerAfterActivityWorkflow)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelTimerAfterActivity(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerStarted(5, 5),
		createTestEventActivityTaskScheduled(6, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "6",
			ActivityType: &commonpb.ActivityType{Name: "testActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventTimerCanceled(7, 5),
		createTestEventActivityTaskStarted(8, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 6,
		}),
		createTestEventActivityTaskCompleted(9, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 6,
			StartedEventId:   8,
		}),
		createTestEventWorkflowTaskScheduled(10, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(11),
		createTestEventWorkflowTaskCompleted(12, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 10,
			StartedEventId:   11,
		}),
		createTestEventWorkflowExecutionCompleted(13, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 12,
		}),
	}
}

func testReplayFailedToStartChildWorkflow(ctx Context) error {
	opts := ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowID:               "workflowId",
	}
	ctx = WithChildWorkflowOptions(ctx, opts)
	err := ExecuteChildWorkflow(ctx, "testWorkflow").GetChildWorkflowExecution().Get(ctx, nil)
	if err != nil {
		var childErr *ChildWorkflowExecutionAlreadyStartedError
		if errors.As(err, &childErr) {
			return nil
		}
		return err
	}
	return errors.New("expected an error, but didn't get one")
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_FailedToStartChildWorkflow() {
	testEvents := createHistoryForFailedToStartChildWorkflow("testReplayFailedToStartChildWorkflow")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayFailedToStartChildWorkflow)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForFailedToStartChildWorkflow(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventStartChildWorkflowExecutionInitiated(5, &historypb.StartChildWorkflowExecutionInitiatedEventAttributes{
			TaskQueue:  &taskqueuepb.TaskQueue{Name: taskQueue},
			WorkflowId: "workflowId",
		}),
		createTestEventStartChildWorkflowExecutionFailed(6, &historypb.StartChildWorkflowExecutionFailedEventAttributes{
			WorkflowId:                   "workflowId",
			InitiatedEventId:             5,
			WorkflowTaskCompletedEventId: 4,
			WorkflowType:                 &commonpb.WorkflowType{Name: "testWorkflow"},
			Cause:                        enumspb.START_CHILD_WORKFLOW_EXECUTION_FAILED_CAUSE_WORKFLOW_ALREADY_EXISTS,
		}),
		createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(8),
		createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventWorkflowExecutionCompleted(10, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 9,
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
		getLogger().Error("activity failed with error.", tagError, err)
		panic("Failed workflow")
	}
	return err
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelChildWorkflow() {
	testEvents := createHistoryForCancelChildWorkflowTests("testReplayWorkflowCancelChildWorkflow")
	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowCancelChildWorkflow)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func createHistoryForCancelChildWorkflowTests(workflowType string) []*historypb.HistoryEvent {
	taskQueue := "taskQueue1"
	return []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: workflowType},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
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

		createTestEventWorkflowTaskScheduled(8, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(9),
		createTestEventWorkflowTaskCompleted(10, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerFired(11, 6),
		createTestEventWorkflowTaskScheduled(12, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(13),
		createTestEventWorkflowTaskCompleted(14, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventRequestCancelExternalWorkflowExecutionInitiated(15, &historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes{
			WorkflowTaskCompletedEventId: 14,
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
		createTestEventWorkflowTaskScheduled(18, &historypb.WorkflowTaskScheduledEventAttributes{}),

		createTestEventActivityTaskStarted(19, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 16,
		}),
		createTestEventWorkflowTaskStarted(20),
		createTestEventWorkflowTaskCompleted(21, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventActivityTaskCompleted(22, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   19,
		}),

		createTestEventChildWorkflowExecutionCanceled(23, &historypb.ChildWorkflowExecutionCanceledEventAttributes{
			InitiatedEventId:  5,
			StartedEventId:    7,
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventWorkflowTaskScheduled(24, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(25),
		createTestEventWorkflowTaskCompleted(26, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 24,
			StartedEventId:   25,
		}),
		createTestEventWorkflowExecutionCompleted(27, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 26,
		}),
	}
}

func testReplayWorkflowCancelChildWorkflowUnusualOrdering(ctx Context) error {
	childCtx1, cancelFunc1 := WithCancel(ctx)

	opts := ChildWorkflowOptions{
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowExecutionTimeout: 10 * time.Second,
		WorkflowID:               "workflowId",
	}
	childCtx1 = WithChildWorkflowOptions(childCtx1, opts)
	cw := ExecuteChildWorkflow(childCtx1, "testWorkflow")
	_ = Sleep(ctx, 1*time.Second)
	cancelFunc1()
	_ = cw.Get(ctx, nil)

	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_ChildWorkflowCancellation_Unusual_Ordering() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowCancelChildWorkflowUnusualOrdering"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
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

		createTestEventWorkflowTaskScheduled(8, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(9),
		createTestEventWorkflowTaskCompleted(10, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerFired(11, 6),
		createTestEventWorkflowTaskScheduled(12, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(13),
		createTestEventWorkflowTaskCompleted(14, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventRequestCancelExternalWorkflowExecutionInitiated(15, &historypb.RequestCancelExternalWorkflowExecutionInitiatedEventAttributes{
			WorkflowTaskCompletedEventId: 14,
			WorkflowExecution:            &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventChildWorkflowExecutionCanceled(16, &historypb.ChildWorkflowExecutionCanceledEventAttributes{
			InitiatedEventId:  5,
			StartedEventId:    7,
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
		}),

		createTestEventExternalWorkflowExecutionCancelRequested(17, &historypb.ExternalWorkflowExecutionCancelRequestedEventAttributes{
			WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflowId"},
			InitiatedEventId:  15,
		}),
		createTestEventWorkflowTaskScheduled(18, &historypb.WorkflowTaskScheduledEventAttributes{}),

		createTestEventActivityTaskStarted(19, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 16,
		}),
		createTestEventWorkflowTaskStarted(20),
		createTestEventWorkflowTaskCompleted(21, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventActivityTaskCompleted(22, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   19,
		}),

		createTestEventWorkflowTaskScheduled(23, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(24),
		createTestEventWorkflowTaskCompleted(25, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 23,
			StartedEventId:   24,
		}),
		createTestEventWorkflowExecutionCompleted(26, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 25,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowCancelChildWorkflowUnusualOrdering)
	err = replayer.ReplayWorkflowHistory(logger, history)
	if err != nil {
		fmt.Printf("replay failed.  Error: %v", err.Error())
	}
	require.NoError(s.T(), err)
}

func testReplayWorkflowCancelWorkflowWhileSleepingWithActivities(ctx Context) error {
	defer func() {
		// When workflow is canceled, it has to get a new disconnected context to execute any activities
		newCtx, _ := NewDisconnectedContext(ctx)
		err := ExecuteActivity(newCtx, testInfiniteActivity).Get(ctx, nil)
		if err != nil {
			panic("Cleanup activity errored")
		}
	}()

	if err := Sleep(ctx, time.Minute*1); err != nil {
		return err
	}

	// This is the activity that should get cancelled
	_ = ExecuteActivity(ctx, "testActivityNoResult").Get(ctx, nil)

	_ = ExecuteActivity(ctx, "testActivityNoResult").Get(ctx, nil)

	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_CancelWorkflowWhileSleepingWithActivities() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{

		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowCancelWorkflowWhileSleepingWithActivities"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerStarted(5, 5),
		createTestEventWorkflowExecutionCancelRequested(6, &historypb.WorkflowExecutionCancelRequestedEventAttributes{}),
		createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(8),
		createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerCanceled(10, 5),
		createTestEventActivityTaskScheduled(11, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "11",
			ActivityType: &commonpb.ActivityType{Name: "testInfiniteActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(12, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 11,
		}),
		createTestEventActivityTaskCompleted(13, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 11,
			StartedEventId:   12,
		}),
		createTestEventWorkflowTaskScheduled(14, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(15),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowCancelWorkflowWhileSleepingWithActivities)
	err = replayer.ReplayWorkflowHistory(logger, history)
	if err != nil {
		fmt.Printf("replay failed.  Error: %v", err.Error())
	}
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Result_Mismatch() {
	taskQueue := "taskQueue1"
	result, _ := converter.GetDefaultDataConverter().ToPayloads("some-incorrect-result")
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowLocalActivity"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventMarkerRecorded(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("1"),
			WorkflowTaskCompletedEventId: 4,
		}),
		createTestEventMarkerRecorded(6, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("2"),
			WorkflowTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(7, &historypb.WorkflowExecutionCompletedEventAttributes{
			Result:                       result,
			WorkflowTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowLocalActivity)
	err = replayer.ReplayWorkflowHistory(logger, history)
	if err != nil {
		fmt.Printf("replay failed.  Error: %v", err.Error())
	}
	require.Error(s.T(), err)
	require.True(s.T(), strings.HasPrefix(err.Error(), "replay workflow doesn't return the same result as the last event"))
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_LocalActivity_Activity_Type_Mismatch() {
	taskQueue := "taskQueue1"
	result, _ := converter.GetDefaultDataConverter().ToPayloads("some-incorrect-result")
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "go.temporal.io/sdk/internal.testReplayWorkflow"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),

		createTestEventMarkerRecorded(5, &historypb.MarkerRecordedEventAttributes{
			MarkerName:                   localActivityMarkerName,
			Details:                      s.createLocalActivityMarkerDataForTest("0"),
			WorkflowTaskCompletedEventId: 4,
		}),

		createTestEventWorkflowExecutionCompleted(6, &historypb.WorkflowExecutionCompletedEventAttributes{
			Result:                       result,
			WorkflowTaskCompletedEventId: 4,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflow)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.Error(s.T(), err)
}

func testReplayWorkflowSideEffect(ctx Context) error {
	if err := Sleep(ctx, time.Second); err != nil {
		return err
	}

	encodedRandom := SideEffect(ctx, func(ctx Context) interface{} {
		return 100
	})

	var random int
	if err := encodedRandom.Get(&random); err != nil {
		return err
	}
	if random == 100 {
		ao := ActivityOptions{
			ScheduleToStartTimeout: time.Second,
			StartToCloseTimeout:    time.Second,
		}
		ctx = WithActivityOptions(ctx, ao)

		a1F := ExecuteActivity(ctx, "A1", "first")
		a2F := ExecuteActivity(ctx, "A2", "second")
		a3F := ExecuteActivity(ctx, "A3", "third")

		var A1Result string
		if err := a1F.Get(ctx, &A1Result); err != nil {
			return err
		}

		err := a3F.Get(ctx, nil)
		if err == nil {
			getLogger().Info("activity A3 completed.")
		}

		err = a2F.Get(ctx, nil)
		if err != nil {
			getLogger().Info("activity A2 completed.")
		}

	}

	getLogger().Info("workflow completed.")
	return nil
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistory_SideEffect() {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			WorkflowType: &commonpb.WorkflowType{Name: "testReplayWorkflowSideEffect"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:        testEncodeFunctionArgs(converter.GetDefaultDataConverter()),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
		createTestEventWorkflowTaskCompleted(4, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventTimerStarted(5, 5),
		createTestEventTimerFired(6, 5),
		createTestEventWorkflowTaskScheduled(7, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(8),
		createTestEventWorkflowTaskCompleted(9, &historypb.WorkflowTaskCompletedEventAttributes{}),
		createTestEventSideEffectMarker(10, 9, 1, 100),

		createTestEventActivityTaskScheduled(11, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "11",
			ActivityType: &commonpb.ActivityType{Name: "A1"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskScheduled(12, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "12",
			ActivityType: &commonpb.ActivityType{Name: "A2"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskScheduled(13, &historypb.ActivityTaskScheduledEventAttributes{
			ActivityId:   "13",
			ActivityType: &commonpb.ActivityType{Name: "A3"},
			TaskQueue:    &taskqueuepb.TaskQueue{Name: taskQueue},
		}),
		createTestEventActivityTaskStarted(14, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 11,
		}),
		createTestEventActivityTaskCompleted(15, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 11,
			StartedEventId:   14,
		}),
		createTestEventWorkflowTaskScheduled(16, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(17),
		createTestEventWorkflowTaskCompleted(18, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 16,
			StartedEventId:   17,
		}),
		createTestEventActivityTaskStarted(19, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 13,
		}),
		createTestEventActivityTaskCompleted(20, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 13,
			StartedEventId:   19,
		}),
		createTestEventWorkflowTaskScheduled(21, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventActivityTaskStarted(22, &historypb.ActivityTaskStartedEventAttributes{
			ScheduledEventId: 12,
		}),
		createTestEventActivityTaskCompleted(23, &historypb.ActivityTaskCompletedEventAttributes{
			ScheduledEventId: 12,
			StartedEventId:   22,
		}),
		createTestEventWorkflowTaskTimedOut(24, &historypb.WorkflowTaskTimedOutEventAttributes{
			ScheduledEventId: 21,
			StartedEventId:   0,
			TimeoutType:      enumspb.TIMEOUT_TYPE_SCHEDULE_TO_START,
		}),
		createTestEventWorkflowTaskScheduled(25, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(26),
		createTestEventWorkflowTaskCompleted(27, &historypb.WorkflowTaskCompletedEventAttributes{
			ScheduledEventId: 25,
			StartedEventId:   26,
		}),
		createTestEventWorkflowExecutionCompleted(28, &historypb.WorkflowExecutionCompletedEventAttributes{
			WorkflowTaskCompletedEventId: 27,
		}),
	}

	history := &historypb.History{Events: testEvents}
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowSideEffect)
	err = replayer.ReplayWorkflowHistory(logger, history)
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFileParent() {
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowFromFileParent)
	err = replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/parentWF.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) TestReplayWorkflowHistoryFromFile() {
	logger := getLogger()
	replayer, err := NewWorkflowReplayer(WorkflowReplayerOptions{})
	require.NoError(s.T(), err)
	replayer.RegisterWorkflow(testReplayWorkflowFromFile)
	err = replayer.ReplayWorkflowHistoryFromJSONFile(logger, "testdata/sampleHistory.json")
	require.NoError(s.T(), err)
}

func (s *internalWorkerTestSuite) testWorkflowTaskHandlerHelper(params workerExecutionParameters) {
	taskQueue := "taskQueue1"
	testEvents := []*historypb.HistoryEvent{
		createTestEventWorkflowExecutionStarted(1, &historypb.WorkflowExecutionStartedEventAttributes{
			TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue},
			Input:     testEncodeFunctionArgs(params.DataConverter),
		}),
		createTestEventWorkflowTaskScheduled(2, &historypb.WorkflowTaskScheduledEventAttributes{}),
		createTestEventWorkflowTaskStarted(3),
	}

	workflowType := "testReplayWorkflow"
	workflowID := "testID"
	runID := "testRunID"

	task := &workflowservice.PollWorkflowTaskQueueResponse{
		WorkflowExecution:      &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
		WorkflowType:           &commonpb.WorkflowType{Name: workflowType},
		History:                &historypb.History{Events: testEvents},
		PreviousStartedEventId: 0,
	}

	r := newWorkflowTaskHandler(params, nil, s.registry)
	_, err := r.ProcessWorkflowTask(&workflowTask{task: task}, nil)
	s.NoError(err)
}

func (s *internalWorkerTestSuite) TestWorkflowTaskHandlerWithDataConverter() {
	cache := NewWorkerCache()
	params := workerExecutionParameters{
		Namespace:     testNamespace,
		Identity:      "identity",
		Logger:        getLogger(),
		DataConverter: iconverter.NewTestDataConverter(),
		cache:         cache,
	}
	s.testWorkflowTaskHandlerHelper(params)
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
func testActivityMultipleArgsWithStruct(_ context.Context, i int, s *testActivityArg) ([]byte, error) {
	fmt.Printf("Executing testActivityMultipleArgsWithStruct: %d, %v\n", i, s)
	return nil, nil
}

func (s *internalWorkerTestSuite) TestCreateWorker() {
	worker := createWorkerWithThrottle(s.service, 500.0, nil)
	worker.RegisterActivity(testActivityNoResult)
	worker.RegisterWorkflow(testWorkflowReturnStruct)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	assert.True(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.True(s.T(), worker.workflowWorker.worker.isWorkerStarted)
	worker.Stop()
	assert.False(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.False(s.T(), worker.workflowWorker.worker.isWorkerStarted)
}

func (s *internalWorkerTestSuite) TestCreateWorkerWithDataConverter() {
	worker := createWorkerWithDataConverter(s.service)
	worker.RegisterActivity(testActivityNoResult)
	worker.RegisterWorkflow(testWorkflowReturnStruct)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	assert.True(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.True(s.T(), worker.workflowWorker.worker.isWorkerStarted)
	worker.Stop()
}

func (s *internalWorkerTestSuite) TestCreateWorkerRun() {
	// Windows doesn't support signalling interrupt.
	if runtime.GOOS == "windows" {
		s.T().Skip("Not supported on windows")
	}
	// Create service endpoint
	mockCtrl := gomock.NewController(s.T())
	service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

	worker := createWorker(service)
	worker.RegisterActivity(testActivityNoResult)
	worker.RegisterWorkflow(testWorkflowReturnStruct)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = worker.Run(InterruptCh())
	}()
	time.Sleep(time.Millisecond * 200)
	p, err := os.FindProcess(os.Getpid())
	assert.NoError(s.T(), err)
	assert.NoError(s.T(), p.Signal(os.Interrupt))
	wg.Wait()
	assert.False(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.False(s.T(), worker.workflowWorker.worker.isWorkerStarted)
}

func (s *internalWorkerTestSuite) TestNoActivitiesOrWorkflows() {
	t := s.T()
	w := createWorker(s.service)
	w.registry = newRegistry()
	assert.Empty(t, w.registry.getRegisteredActivities())
	assert.Empty(t, w.registry.getRegisteredWorkflowTypes())
	assert.NoError(t, w.Start())
	assert.True(t, w.activityWorker.worker.isWorkerStarted)
	assert.True(t, w.workflowWorker.worker.isWorkerStarted)
}

func (s *internalWorkerTestSuite) TestStartWorkerAfterStopped() {
	defer func() {
		if r := recover(); r == nil {
			assert.Fail(s.T(), "calling start after stop must result in panic")
		}
	}()
	worker := createWorkerWithThrottle(s.service, 500.0, nil)
	worker.RegisterActivity(testActivityNoResult)
	worker.RegisterWorkflow(testWorkflowReturnStruct)
	err := worker.Start()
	require.NoError(s.T(), err)
	time.Sleep(time.Millisecond * 200)
	assert.True(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.True(s.T(), worker.workflowWorker.worker.isWorkerStarted)
	worker.Stop()
	assert.False(s.T(), worker.activityWorker.worker.isWorkerStarted)
	assert.False(s.T(), worker.workflowWorker.worker.isWorkerStarted)
	_ = worker.Start() // must panic
}

func ofPollActivityTaskQueueRequest(tps float64) gomock.Matcher {
	return &mockPollActivityTaskQueueRequest{tps: tps}
}

type mockPollActivityTaskQueueRequest struct {
	tps float64
}

func (m *mockPollActivityTaskQueueRequest) Matches(x interface{}) bool {
	v, ok := x.(*workflowservice.PollActivityTaskQueueRequest)
	if !ok {
		return false
	}

	if v.TaskQueueMetadata != nil && v.TaskQueueMetadata.MaxTasksPerSecond != nil {
		return v.TaskQueueMetadata.MaxTasksPerSecond.GetValue() == m.tps
	}

	return false
}

func (m *mockPollActivityTaskQueueRequest) String() string {
	return "PollActivityTaskQueueRequest"
}

func createWorker(service *workflowservicemock.MockWorkflowServiceClient) *AggregatedWorker {
	return createWorkerWithThrottle(service, 0.0, nil)
}

func createWorkerWithThrottle(
	service *workflowservicemock.MockWorkflowServiceClient, activitiesPerSecond float64, dc converter.DataConverter,
) *AggregatedWorker {
	namespace := "testNamespace"
	namespaceState := enumspb.NAMESPACE_STATE_REGISTERED
	namespaceDesc := &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespacepb.NamespaceInfo{
			Name:  namespace,
			State: namespaceState,
		},
	}
	// mocks
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).Return(namespaceDesc, nil).Do(
		func(ctx context.Context, request *workflowservice.DescribeNamespaceRequest, opts ...grpc.CallOption) {
			// log
		}).AnyTimes()

	activityTask := &workflowservice.PollActivityTaskQueueResponse{}
	expectedActivitiesPerSecond := activitiesPerSecond
	if expectedActivitiesPerSecond == 0.0 {
		expectedActivitiesPerSecond = defaultTaskQueueActivitiesPerSecond
	}
	service.EXPECT().PollActivityTaskQueue(
		gomock.Any(), ofPollActivityTaskQueueRequest(expectedActivitiesPerSecond), gomock.Any(),
	).Return(activityTask, nil).AnyTimes()
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.RespondActivityTaskCompletedResponse{}, nil).AnyTimes()

	workflowTask := &workflowservice.PollWorkflowTaskQueueResponse{}
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return(workflowTask, nil).AnyTimes()
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

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
	return createWorkerWithThrottle(service, 0.0, iconverter.NewTestDataConverter())
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
	opt := ClientOptions{DataConverter: iconverter.NewTestDataConverter()}
	s.testCompleteActivityHelper(opt)
}

func (s *internalWorkerTestSuite) TestCompleteActivityWithContextAwareDataConverter() {
	dc := NewContextAwareDataConverter(converter.GetDefaultDataConverter())
	client := NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})

	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "e")

	response := &workflowservice.RespondActivityTaskCompletedResponse{}

	s.service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.RespondActivityTaskCompletedRequest, _ ...interface{}) {
			dc := client.dataConverter
			results := dc.ToStrings(req.Result)
			s.Equal("\"t?st\"", results[0])
		})

	_ = client.CompleteActivity(ctx, []byte("task-token"), "test", nil)
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

func (s *internalWorkerTestSuite) TestCompleteActivityByIDWithContextAwareDataConverter() {
	dc := NewContextAwareDataConverter(converter.GetDefaultDataConverter())
	client := NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})

	ctx := context.Background()
	ctx = context.WithValue(ctx, ContextAwareDataConverterContextKey, "e")

	response := &workflowservice.RespondActivityTaskCompletedByIdResponse{}

	s.service.EXPECT().RespondActivityTaskCompletedById(gomock.Any(), gomock.Any(), gomock.Any()).Return(response, nil).
		Do(func(_ interface{}, req *workflowservice.RespondActivityTaskCompletedByIdRequest, _ ...interface{}) {
			dc := client.dataConverter
			results := dc.ToStrings(req.Result)
			s.Equal("\"t?st\"", results[0])
		})

	_ = client.CompleteActivityByID(ctx, DefaultNamespace, "wid", "", "aid", "test", nil)
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
	dc := iconverter.NewTestDataConverter()
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
	type exeType func(activity interface{}, args ...interface{}) Future
	type exeAndCtx struct {
		exe exeType
		ctx Context
	}

	ao := ActivityOptions{
		ScheduleToStartTimeout: 10 * time.Second,
		StartToCloseTimeout:    5 * time.Second,
	}
	nonlocalCtx := WithActivityOptions(ctx, ao)
	nonlocalExecutor := func(activity interface{}, args ...interface{}) Future {
		return ExecuteActivity(nonlocalCtx, activity, args...)
	}
	localOptions := LocalActivityOptions{
		StartToCloseTimeout: time.Second * 5,
	}
	localCtx := WithLocalActivityOptions(ctx, localOptions)
	localExecutor := func(activity interface{}, args ...interface{}) Future {
		return ExecuteLocalActivity(localCtx, activity, args...)
	}
	nonlocal := exeAndCtx{exe: nonlocalExecutor, ctx: nonlocalCtx}
	local := exeAndCtx{exe: localExecutor, ctx: localCtx}

	for _, executor := range []exeAndCtx{nonlocal, local} {
		// By functions.
		err = executor.exe(testActivityByteArgs, input).Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe(testActivityMultipleArgs, 2, []string{"test"}, true).Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe(testActivityMultipleArgsWithStruct, -8, newTestActivityArg()).Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe(testActivityNoResult, 2, "test").Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe(testActivityNoContextArg, 2, "test").Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		f := executor.exe(testActivityReturnByteArray)
		var r []byte
		err = f.Get(executor.ctx, &r)
		require.NoError(w.t, err, err)
		require.Equal(w.t, []byte("testActivity"), r)

		f = executor.exe(testActivityReturnInt)
		var rInt int
		err = f.Get(executor.ctx, &rInt)
		require.NoError(w.t, err, err)
		require.Equal(w.t, 5, rInt)

		f = executor.exe(testActivityReturnString)
		var rString string
		err = f.Get(executor.ctx, &rString)

		require.NoError(w.t, err, err)
		require.Equal(w.t, "testActivity", rString)

		f = executor.exe(testActivityReturnEmptyString)
		var r2String string
		err = f.Get(executor.ctx, &r2String)
		require.NoError(w.t, err, err)
		require.Equal(w.t, "", r2String)

		f = executor.exe(testActivityReturnEmptyStruct)
		var r2Struct testActivityResult
		err = f.Get(executor.ctx, &r2Struct)
		require.NoError(w.t, err, err)
		require.Equal(w.t, testActivityResult{}, r2Struct)

		f = executor.exe(testActivityReturnNilStructPtr)
		var rStructPtr *testActivityResult
		err = f.Get(executor.ctx, &rStructPtr)
		require.NoError(w.t, err, err)
		require.True(w.t, rStructPtr == nil)

		f = executor.exe(testActivityReturnStructPtr)
		err = f.Get(executor.ctx, &rStructPtr)
		require.NoError(w.t, err, err)
		require.Equal(w.t, *rStructPtr, testActivityResult{Index: 10})

		f = executor.exe(testActivityReturnNilStructPtrPtr)
		var rStruct2Ptr **testActivityResult
		err = f.Get(executor.ctx, &rStruct2Ptr)
		require.NoError(w.t, err, err)
		require.True(w.t, rStruct2Ptr == nil)

		f = executor.exe(testActivityReturnStructPtrPtr)
		err = f.Get(executor.ctx, &rStruct2Ptr)
		require.NoError(w.t, err, err)
		require.True(w.t, **rStruct2Ptr == testActivityResult{Index: 10})

		// By names.
		err = executor.exe("testActivityByteArgs", input).Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe("testActivityMultipleArgs", 2, []string{"test"}, true).Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe("testActivityNoResult", 2, "test").Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		err = executor.exe("testActivityNoContextArg", 2, "test").Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		f = executor.exe("testActivityReturnString")
		err = f.Get(executor.ctx, &rString)
		require.NoError(w.t, err, err)
		require.Equal(w.t, "testActivity", rString, rString)

		f = executor.exe("testActivityReturnEmptyString")
		var r2sString string
		err = f.Get(executor.ctx, &r2String)
		require.NoError(w.t, err, err)
		require.Equal(w.t, "", r2sString)

		f = executor.exe("testActivityReturnEmptyStruct")
		err = f.Get(executor.ctx, &r2Struct)
		require.NoError(w.t, err, err)
		require.Equal(w.t, testActivityResult{}, r2Struct)

		f = executor.exe("testActivityStructWithFns_ValidActivity")
		err = f.Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

		// By struct
		actStruct := &testActivityStruct{}
		f = executor.exe(actStruct.SomeActivity)
		err = f.Get(executor.ctx, nil)
		require.NoError(w.t, err, err)

	}

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

func testInfiniteActivity(ctx context.Context) error {
	for {
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil
		}
	}
}

type testActivityStruct struct{}

func (a *testActivityStruct) SomeActivity() (string, error) {
	return "some activity on a struct", nil
}

type testActivityStructWithFns struct{}

func (t *testActivityStructWithFns) ValidActivity(context.Context) error { return nil }

func (t *testActivityStructWithFns) InvalidActivity(context.Context) {}

func testRegisterStructWithInvalidFnsWithoutSkipFails() {
	registry := newRegistry()
	registry.RegisterActivityWithOptions(&testActivityStructWithFns{}, RegisterActivityOptions{
		Name:                       "testActivityStructWithFns_",
		SkipInvalidStructFunctions: false,
	})
}

func TestRegisterStructWithInvalidFnsWithoutSkipFails(t *testing.T) {
	assert.Panics(t, testRegisterStructWithInvalidFnsWithoutSkipFails)
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
	env.SetDataConverter(iconverter.NewTestDataConverter())
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

func testActivityErrorWithDetailsHelper(ctx context.Context, t *testing.T, dataConverter converter.DataConverter) {
	a1 := activityExecutor{
		name: "test",
		fn: func(arg1 int) (err error) {
			return NewApplicationError("testReason", "", false, nil, "testStringDetails")
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
			return NewApplicationError("testReason", "", false, nil, testErrorDetails{T: "testErrorStack"})
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
			return "testResult", NewApplicationError("testReason", "", false, nil, testErrorDetails{T: "testErrorStack3"})
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
			return "testResult4", NewApplicationError("testReason", "", false, nil, "testMultipleString", testErrorDetails{T: "testErrorStack4"})
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
	dc := iconverter.NewTestDataConverter()
	ctx, _ := newActivityContext(context.Background(), nil, &activityEnvironment{dataConverter: dc})
	testActivityErrorWithDetailsHelper(ctx, t, dc)
}

func testActivityCanceledErrorHelper(ctx context.Context, t *testing.T, dataConverter converter.DataConverter) {
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

func TestActivityCanceledErrorWithDataConverter(t *testing.T) {
	dc := iconverter.NewTestDataConverter()
	ctx, _ := newActivityContext(context.Background(), nil, &activityEnvironment{dataConverter: dc})
	testActivityCanceledErrorHelper(ctx, t, dc)
}

func testActivityExecutionVariousTypesHelper(ctx context.Context, t *testing.T, dataConverter converter.DataConverter) {
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
	dc := iconverter.NewTestDataConverter()
	ctx, _ := newActivityContext(context.Background(), nil, &activityEnvironment{
		dataConverter: dc,
	})
	testActivityExecutionVariousTypesHelper(ctx, t, dc)
}

func TestActivityNilArgs(t *testing.T) {
	nilErr := errors.New("nils")
	activityFn := func(name string, idx int, strptr *string, wt *commonpb.WorkflowType) error {
		if name == "" && idx == 0 && strptr == nil && wt == nil {
			return nilErr
		}
		return nil
	}

	args := []interface{}{nil, nil, nil, nil}
	_, err := getValidatedActivityFunction(activityFn, args, newRegistry())
	require.NoError(t, err)

	dataConverter := converter.GetDefaultDataConverter()
	data, err := encodeArgs(dataConverter, args)
	require.NoError(t, err)

	reflectArgs, err := decodeArgs(dataConverter, reflect.TypeOf(activityFn), data)
	require.NoError(t, err)

	reflectResults := reflect.ValueOf(activityFn).Call(reflectArgs)
	require.Equal(t, nilErr, reflectResults[0].Interface())
}

func TestWorkerOptionInvalid(t *testing.T) {
	require.Panics(t, func() {
		NewAggregatedWorker(&WorkflowClient{}, "worker-options-tq", WorkerOptions{MaxConcurrentWorkflowTaskPollers: 1})
	})
}

func TestWorkerOptionDefaults(t *testing.T) {
	client := &WorkflowClient{}
	taskQueue := "worker-options-tq"
	aggWorker := NewAggregatedWorker(client, taskQueue, WorkerOptions{})

	workflowWorker := aggWorker.workflowWorker
	require.True(t, workflowWorker.executionParameters.Identity != "")
	require.NotNil(t, workflowWorker.executionParameters.Logger)
	require.NotNil(t, workflowWorker.executionParameters.MetricsHandler)
	require.Nil(t, workflowWorker.executionParameters.ContextPropagators)

	expected := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             taskQueue,
		MaxConcurrentActivityTaskQueuePollers: defaultConcurrentPollRoutineSize,
		MaxConcurrentWorkflowTaskQueuePollers: defaultConcurrentPollRoutineSize,
		ConcurrentLocalActivityExecutionSize:  defaultMaxConcurrentLocalActivityExecutionSize,
		ConcurrentActivityExecutionSize:       defaultMaxConcurrentActivityExecutionSize,
		ConcurrentWorkflowTaskExecutionSize:   defaultMaxConcurrentTaskExecutionSize,
		WorkerActivitiesPerSecond:             defaultTaskQueueActivitiesPerSecond,
		TaskQueueActivitiesPerSecond:          defaultTaskQueueActivitiesPerSecond,
		WorkerLocalActivitiesPerSecond:        defaultWorkerLocalActivitiesPerSecond,
		StickyScheduleToStartTimeout:          stickyWorkflowTaskScheduleToStartTimeoutSeconds * time.Second,
		DataConverter:                         converter.GetDefaultDataConverter(),
		Logger:                                workflowWorker.executionParameters.Logger,
		MetricsHandler:                        workflowWorker.executionParameters.MetricsHandler,
		Identity:                              workflowWorker.executionParameters.Identity,
		UserContext:                           workflowWorker.executionParameters.UserContext,
	}

	assertWorkerExecutionParamsEqual(t, expected, workflowWorker.executionParameters)

	activityWorker := aggWorker.activityWorker
	require.True(t, activityWorker.executionParameters.Identity != "")
	require.NotNil(t, activityWorker.executionParameters.Logger)
	require.NotNil(t, activityWorker.executionParameters.MetricsHandler)
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
		dataConverter:      &converter.CompositeDataConverter{},
		contextPropagators: nil,
		logger:             ilog.NewNopLogger(),
	}

	options := WorkerOptions{
		TaskQueueActivitiesPerSecond:            8888,
		MaxConcurrentSessionExecutionSize:       3333,
		MaxConcurrentWorkflowTaskExecutionSize:  2222,
		MaxConcurrentActivityExecutionSize:      1111,
		MaxConcurrentLocalActivityExecutionSize: 101,
		MaxConcurrentWorkflowTaskPollers:        11,
		MaxConcurrentActivityTaskPollers:        12,
		WorkerLocalActivitiesPerSecond:          222,
		WorkerActivitiesPerSecond:               99,
		StickyScheduleToStartTimeout:            555 * time.Minute,
		BackgroundActivityContext:               context.Background(),
	}

	aggWorker := NewAggregatedWorker(client, taskQueue, options)

	workflowWorker := aggWorker.workflowWorker
	require.Len(t, workflowWorker.executionParameters.ContextPropagators, 0)

	expected := workerExecutionParameters{
		TaskQueue:                             taskQueue,
		MaxConcurrentActivityTaskQueuePollers: options.MaxConcurrentActivityTaskPollers,
		MaxConcurrentWorkflowTaskQueuePollers: options.MaxConcurrentWorkflowTaskPollers,
		ConcurrentLocalActivityExecutionSize:  options.MaxConcurrentLocalActivityExecutionSize,
		ConcurrentActivityExecutionSize:       options.MaxConcurrentActivityExecutionSize,
		ConcurrentWorkflowTaskExecutionSize:   options.MaxConcurrentWorkflowTaskExecutionSize,
		WorkerActivitiesPerSecond:             options.WorkerActivitiesPerSecond,
		TaskQueueActivitiesPerSecond:          options.TaskQueueActivitiesPerSecond,
		WorkerLocalActivitiesPerSecond:        options.WorkerLocalActivitiesPerSecond,
		StickyScheduleToStartTimeout:          options.StickyScheduleToStartTimeout,
		DataConverter:                         client.dataConverter,
		Logger:                                client.logger,
		MetricsHandler:                        client.metricsHandler,
		Identity:                              client.identity,
	}

	assertWorkerExecutionParamsEqual(t, expected, workflowWorker.executionParameters)

	activityWorker := aggWorker.activityWorker
	require.Len(t, activityWorker.executionParameters.ContextPropagators, 0)
	assertWorkerExecutionParamsEqual(t, expected, activityWorker.executionParameters)
}

func TestLocalActivityWorkerOnly(t *testing.T) {
	client := &WorkflowClient{}
	taskQueue := "worker-options-tq"
	aggWorker := NewAggregatedWorker(client, taskQueue, WorkerOptions{LocalActivityWorkerOnly: true})

	workflowWorker := aggWorker.workflowWorker
	require.True(t, workflowWorker.executionParameters.Identity != "")
	require.NotNil(t, workflowWorker.executionParameters.Logger)
	require.NotNil(t, workflowWorker.executionParameters.MetricsHandler)
	require.Nil(t, workflowWorker.executionParameters.ContextPropagators)

	expected := workerExecutionParameters{
		Namespace:                             DefaultNamespace,
		TaskQueue:                             taskQueue,
		MaxConcurrentActivityTaskQueuePollers: defaultConcurrentPollRoutineSize,
		MaxConcurrentWorkflowTaskQueuePollers: defaultConcurrentPollRoutineSize,
		ConcurrentLocalActivityExecutionSize:  defaultMaxConcurrentLocalActivityExecutionSize,
		ConcurrentActivityExecutionSize:       defaultMaxConcurrentActivityExecutionSize,
		ConcurrentWorkflowTaskExecutionSize:   defaultMaxConcurrentTaskExecutionSize,
		WorkerActivitiesPerSecond:             defaultTaskQueueActivitiesPerSecond,
		TaskQueueActivitiesPerSecond:          defaultTaskQueueActivitiesPerSecond,
		WorkerLocalActivitiesPerSecond:        defaultWorkerLocalActivitiesPerSecond,
		StickyScheduleToStartTimeout:          stickyWorkflowTaskScheduleToStartTimeoutSeconds * time.Second,
		DataConverter:                         converter.GetDefaultDataConverter(),
		Logger:                                workflowWorker.executionParameters.Logger,
		MetricsHandler:                        workflowWorker.executionParameters.MetricsHandler,
		Identity:                              workflowWorker.executionParameters.Identity,
		UserContext:                           workflowWorker.executionParameters.UserContext,
	}

	assertWorkerExecutionParamsEqual(t, expected, workflowWorker.executionParameters)

	activityWorker := aggWorker.activityWorker
	require.Nil(t, activityWorker)
	sessionWorker := aggWorker.sessionWorker
	require.Nil(t, sessionWorker)
}

func assertWorkerExecutionParamsEqual(t *testing.T, paramsA workerExecutionParameters, paramsB workerExecutionParameters) {
	require.Equal(t, paramsA.TaskQueue, paramsA.TaskQueue)
	require.Equal(t, paramsA.Identity, paramsB.Identity)
	require.Equal(t, paramsA.DataConverter, paramsB.DataConverter)
	require.Equal(t, paramsA.ConcurrentLocalActivityExecutionSize, paramsB.ConcurrentLocalActivityExecutionSize)
	require.Equal(t, paramsA.ConcurrentActivityExecutionSize, paramsB.ConcurrentActivityExecutionSize)
	require.Equal(t, paramsA.ConcurrentWorkflowTaskExecutionSize, paramsB.ConcurrentWorkflowTaskExecutionSize)
	require.Equal(t, paramsA.WorkerActivitiesPerSecond, paramsB.WorkerActivitiesPerSecond)
	require.Equal(t, paramsA.TaskQueueActivitiesPerSecond, paramsB.TaskQueueActivitiesPerSecond)
	require.Equal(t, paramsA.StickyScheduleToStartTimeout, paramsB.StickyScheduleToStartTimeout)
	require.Equal(t, paramsA.MaxConcurrentWorkflowTaskQueuePollers, paramsB.MaxConcurrentWorkflowTaskQueuePollers)
	require.Equal(t, paramsA.MaxConcurrentActivityTaskQueuePollers, paramsB.MaxConcurrentActivityTaskQueuePollers)
	require.Equal(t, paramsA.WorkflowPanicPolicy, paramsB.WorkflowPanicPolicy)
	require.Equal(t, paramsA.EnableLoggingInReplay, paramsB.EnableLoggingInReplay)
}

// Encode function args
func testEncodeFunctionArgs(dataConverter converter.DataConverter, args ...interface{}) *commonpb.Payloads {
	input, err := encodeArgs(dataConverter, args)
	if err != nil {
		fmt.Println(err)
		panic("Failed to encode arguments")
	}
	return input
}

func TestIsNonRetriableError(t *testing.T) {
	tests := []struct {
		err      error
		expected bool
	}{
		{
			err:      nil,
			expected: false,
		},
		{
			err:      serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""),
			expected: false,
		},
		{
			err:      serviceerror.NewInvalidArgument(""),
			expected: true,
		},
		{
			err:      serviceerror.NewClientVersionNotSupported("", "", ""),
			expected: true,
		},
	}

	for _, test := range tests {
		require.Equal(t, test.expected, isNonRetriableError(test.err))
	}
}

func TestWorkerRegisterDisabledWorkflow(t *testing.T) {
	// Expect panic
	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		worker := NewAggregatedWorker(&WorkflowClient{}, "some-task-queue", WorkerOptions{DisableWorkflowWorker: true})
		worker.RegisterWorkflow(testReplayWorkflow)
	}()
	require.Equal(t, "workflow worker disabled, cannot register workflow", recovered)
}
