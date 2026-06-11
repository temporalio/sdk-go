package internal

import (
	"context"
	iconverter "go.temporal.io/sdk/internal/converter"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
)

const (
	scheduleID = "some random schedule ID"
)

// schedule client test suite
type (
	scheduleClientTestSuite struct {
		suite.Suite
		mockCtrl      *gomock.Controller
		service       *workflowservicemock.MockWorkflowServiceClient
		client        Client
		dataConverter converter.DataConverter
	}
)

func TestScheduleClientSuite(t *testing.T) {
	suite.Run(t, new(scheduleClientTestSuite))
}

func (s *scheduleClientTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	s.client = NewServiceClient(s.service, nil, ClientOptions{})
	s.dataConverter = converter.GetDefaultDataConverter()
}

func (s *scheduleClientTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *scheduleClientTestSuite) TestCreateScheduleClient() {
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	options := ScheduleOptions{
		ID: scheduleID,
		Spec: ScheduleSpec{
			CronExpressions: []string{"*"},
		},
		Action: &ScheduleWorkflowAction{
			Workflow:                 wf,
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds,
			WorkflowTaskTimeout:      timeoutInSeconds,
		},
	}
	createResp := &workflowservice.CreateScheduleResponse{}
	s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResp, nil).Times(1)

	scheduleHandle, err := s.client.ScheduleClient().Create(context.Background(), options)
	s.Nil(err)
	s.Equal(scheduleHandle.GetID(), scheduleID)
}

func (s *scheduleClientTestSuite) TestCreateScheduleNoID() {
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	options := ScheduleOptions{
		Spec: ScheduleSpec{
			CronExpressions: []string{"*"},
		},
		Action: &ScheduleWorkflowAction{
			Workflow:                 wf,
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds,
			WorkflowTaskTimeout:      timeoutInSeconds,
		},
	}

	_, err := s.client.ScheduleClient().Create(context.Background(), options)
	s.NotNil(err)
}

func (s *scheduleClientTestSuite) TestCreateScheduleWithMemoAndSearchAttr() {
	memo := map[string]interface{}{
		"testMemo": "memo value",
	}
	searchAttributes := map[string]interface{}{
		"testAttr": "attr value",
	}

	wf := func(ctx Context) string {
		panic("this is just a stub")
	}

	options := ScheduleOptions{
		ID: scheduleID,
		Spec: ScheduleSpec{
			CronExpressions: []string{"*"},
		},
		Action: &ScheduleWorkflowAction{
			Workflow:                 wf,
			ID:                       "wid",
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds,
			WorkflowTaskTimeout:      timeoutInSeconds,
		},
		Memo:             memo,
		SearchAttributes: searchAttributes,
	}
	createResp := &workflowservice.CreateScheduleResponse{}

	s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResp, nil).
		Do(func(_ interface{}, req *workflowservice.CreateScheduleRequest, _ ...interface{}) {
			var resultMemo, resultAttr string
			// verify the schedules memo and search attributes
			err := converter.GetDefaultDataConverter().FromPayload(req.Memo.Fields["testMemo"], &resultMemo)
			s.NoError(err)
			s.Equal("memo value", resultMemo)

			err = converter.GetDefaultDataConverter().FromPayload(req.SearchAttributes.IndexedFields["testAttr"], &resultAttr)
			s.NoError(err)
			s.Equal("attr value", resultAttr)
		})
	_, _ = s.client.ScheduleClient().Create(context.Background(), options)
}

func getListSchedulesRequest() *workflowservice.ListSchedulesRequest {
	request := &workflowservice.ListSchedulesRequest{
		Namespace: DefaultNamespace,
	}

	return request
}

// ScheduleIterator

func (s *scheduleClientTestSuite) TestScheduleIterator_NoError() {
	request1 := getListSchedulesRequest()
	response1 := &workflowservice.ListSchedulesResponse{
		Schedules: []*schedulepb.ScheduleListEntry{
			{
				ScheduleId: "",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getListSchedulesRequest()
	request2.NextPageToken = response1.NextPageToken
	response2 := &workflowservice.ListSchedulesResponse{
		Schedules: []*schedulepb.ScheduleListEntry{
			{
				ScheduleId: "",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	request3 := getListSchedulesRequest()
	request3.NextPageToken = response2.NextPageToken
	response3 := &workflowservice.ListSchedulesResponse{
		Schedules: []*schedulepb.ScheduleListEntry{
			{
				ScheduleId: "",
			},
		},
		NextPageToken: nil,
	}

	s.service.EXPECT().ListSchedules(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)
	s.service.EXPECT().ListSchedules(gomock.Any(), request2, gomock.Any()).Return(response2, nil).Times(1)
	s.service.EXPECT().ListSchedules(gomock.Any(), request3, gomock.Any()).Return(response3, nil).Times(1)

	var events []*ScheduleListEntry
	iter, _ := s.client.ScheduleClient().List(context.Background(), ScheduleListOptions{})
	for iter.HasNext() {
		event, err := iter.Next()
		s.Nil(err)
		events = append(events, event)
	}
	s.Equal(3, len(events))
}

func (s *scheduleClientTestSuite) TestIteratorError() {
	request1 := getListSchedulesRequest()
	response1 := &workflowservice.ListSchedulesResponse{
		Schedules: []*schedulepb.ScheduleListEntry{
			{
				ScheduleId: "",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getListSchedulesRequest()
	request2.NextPageToken = response1.NextPageToken

	s.service.EXPECT().ListSchedules(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)

	iter, _ := s.client.ScheduleClient().List(context.Background(), ScheduleListOptions{})

	s.True(iter.HasNext())
	event, err := iter.Next()
	s.NotNil(event)
	s.Nil(err)

	s.service.EXPECT().ListSchedules(gomock.Any(), request2, gomock.Any()).Return(nil, serviceerror.NewNotFound("")).Times(1)

	s.True(iter.HasNext())
	event, err = iter.Next()
	s.Nil(event)
	s.NotNil(err)
}

func (s *scheduleClientTestSuite) TestCreateScheduleWorkflowMemoDataConverter() {
	testFn := func() {
		dc := iconverter.NewTestDataConverter()
		s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})

		memo := map[string]interface{}{
			"testMemo": "memo value",
		}
		wf := func(ctx Context) string { panic("this is just a stub") }

		options := ScheduleOptions{
			ID: scheduleID,
			Spec: ScheduleSpec{
				CronExpressions: []string{"*"},
			},
			Action: &ScheduleWorkflowAction{
				Workflow:                 wf,
				ID:                       workflowID,
				TaskQueue:                taskqueue,
				WorkflowExecutionTimeout: timeoutInSeconds,
				WorkflowTaskTimeout:      timeoutInSeconds,
				Memo:                     memo,
			},
		}
		createResp := &workflowservice.CreateScheduleResponse{}
		s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResp, nil).
			Do(func(_ interface{}, req *workflowservice.CreateScheduleRequest, _ ...interface{}) {
				startWorkflow := req.Schedule.Action.GetStartWorkflow()
				encoding := string(startWorkflow.Memo.Fields["testMemo"].Metadata[converter.MetadataEncoding])
				if sdkFlagsAllowed[SDKFlagMemoUserDCEncode] {
					s.Equal("binary/gob", encoding)
				} else {
					s.Equal("json/plain", encoding)
				}
			})

		_, err := s.client.ScheduleClient().Create(context.Background(), options)
		s.NoError(err)
	}
	s.T().Run("old behavior", func(t *testing.T) {
		orig := sdkFlagsAllowed[SDKFlagMemoUserDCEncode]
		sdkFlagsAllowed[SDKFlagMemoUserDCEncode] = false
		defer func() { sdkFlagsAllowed[SDKFlagMemoUserDCEncode] = orig }()
		testFn()
	})
	s.T().Run("default behavior", func(t *testing.T) {
		s.True(sdkFlagsAllowed[SDKFlagMemoUserDCEncode])
		testFn()
	})
}

func (s *scheduleClientTestSuite) TestCreateScheduleWorkflowMemoUserAndDefaultConverterFail() {
	testFn := func() {
		dc := failingMemoDataConverter{
			delegate: converter.GetDefaultDataConverter(),
		}
		s.client = NewServiceClient(s.service, nil, ClientOptions{DataConverter: dc})

		memo := map[string]interface{}{
			"testMemo": make(chan int),
		}
		wf := func(ctx Context) string { panic("this is just a stub") }

		options := ScheduleOptions{
			ID: scheduleID,
			Spec: ScheduleSpec{
				CronExpressions: []string{"*"},
			},
			Action: &ScheduleWorkflowAction{
				Workflow:                 wf,
				ID:                       workflowID,
				TaskQueue:                taskqueue,
				WorkflowExecutionTimeout: timeoutInSeconds,
				WorkflowTaskTimeout:      timeoutInSeconds,
				Memo:                     memo,
			},
		}

		s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		_, err := s.client.ScheduleClient().Create(context.Background(), options)
		s.Error(err)
		if sdkFlagsAllowed[SDKFlagMemoUserDCEncode] {
			s.ErrorContains(err, "failingMemoDataConverter memo encoding failed")
		} else {
			s.ErrorContains(err, "unsupported type: chan int")
		}
	}

	s.T().Run("old behavior", func(t *testing.T) {
		orig := sdkFlagsAllowed[SDKFlagMemoUserDCEncode]
		sdkFlagsAllowed[SDKFlagMemoUserDCEncode] = false
		defer func() { sdkFlagsAllowed[SDKFlagMemoUserDCEncode] = orig }()
		testFn()
	})
	s.T().Run("default behavior", func(t *testing.T) {
		s.True(sdkFlagsAllowed[SDKFlagMemoUserDCEncode])
		testFn()
	})
}

// newDescribeResponseWithPriority builds a mock DescribeScheduleResponse with
// the given priority set on the StartWorkflow action. This avoids duplicating
// the protobuf scaffolding across multiple tests.
func newDescribeResponseWithPriority(priority *commonpb.Priority) *workflowservice.DescribeScheduleResponse {
	return &workflowservice.DescribeScheduleResponse{
		Schedule: &schedulepb.Schedule{
			Spec: &schedulepb.ScheduleSpec{
				CronString: []string{"*"},
			},
			Action: &schedulepb.ScheduleAction{
				Action: &schedulepb.ScheduleAction_StartWorkflow{
					StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
						WorkflowId: workflowID,
						WorkflowType: &commonpb.WorkflowType{
							Name: "test-workflow",
						},
						TaskQueue: &taskqueuepb.TaskQueue{
							Name: taskqueue,
						},
						Priority: priority,
					},
				},
			},
			Policies: &schedulepb.SchedulePolicies{},
		},
		Info: &schedulepb.ScheduleInfo{},
	}
}

func (s *scheduleClientTestSuite) TestCreateAndDescribeScheduleWithPriority() {
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	priority := Priority{
		PriorityKey:    2,
		FairnessKey:    "test-fairness-key",
		FairnessWeight: 1.5,
	}
	options := ScheduleOptions{
		ID: scheduleID,
		Spec: ScheduleSpec{
			CronExpressions: []string{"*"},
		},
		Action: &ScheduleWorkflowAction{
			Workflow:                 wf,
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds,
			WorkflowTaskTimeout:      timeoutInSeconds,
			Priority:                 priority,
		},
	}
	createResp := &workflowservice.CreateScheduleResponse{}
	s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(_ interface{}, req *workflowservice.CreateScheduleRequest, _ ...interface{}) {
			// Verify that the Priority was serialized correctly into CreateScheduleRequest
			action := req.Schedule.Action.GetStartWorkflow()
			s.NotNil(action)
			s.Equal(int32(2), action.Priority.PriorityKey)
			s.Equal("test-fairness-key", action.Priority.FairnessKey)
			s.Equal(float32(1.5), action.Priority.FairnessWeight)
		}).
		Return(createResp, nil).
		Times(1)

	scheduleHandle, err := s.client.ScheduleClient().Create(context.Background(), options)
	s.Nil(err)
	s.Equal(scheduleHandle.GetID(), scheduleID)

	// Now mock DescribeSchedule to return the schedule with the same priority
	describeResp := newDescribeResponseWithPriority(&commonpb.Priority{
		PriorityKey:    2,
		FairnessKey:    "test-fairness-key",
		FairnessWeight: 1.5,
	})
	s.service.EXPECT().DescribeSchedule(gomock.Any(), &workflowservice.DescribeScheduleRequest{
		Namespace:  DefaultNamespace,
		ScheduleId: scheduleID,
	}, gomock.Any()).Return(describeResp, nil).Times(1)

	desc, err := scheduleHandle.Describe(context.Background())
	s.Nil(err)
	s.NotNil(desc)

	actionDesc, ok := desc.Schedule.Action.(*ScheduleWorkflowAction)
	s.True(ok)
	s.NotNil(actionDesc)
	s.Equal(priority, actionDesc.Priority)
}

func (s *scheduleClientTestSuite) TestUpdateSchedulePreservesPriority() {
	// Setup: Create a schedule with priority
	wf := func(ctx Context) string {
		panic("this is just a stub")
	}
	priority := Priority{
		PriorityKey:    2,
		FairnessKey:    "test-fairness-key",
		FairnessWeight: 1.5,
	}
	options := ScheduleOptions{
		ID: scheduleID,
		Spec: ScheduleSpec{
			CronExpressions: []string{"*"},
		},
		Action: &ScheduleWorkflowAction{
			Workflow:                 wf,
			ID:                       workflowID,
			TaskQueue:                taskqueue,
			WorkflowExecutionTimeout: timeoutInSeconds,
			WorkflowTaskTimeout:      timeoutInSeconds,
			Priority:                 priority,
		},
	}
	createResp := &workflowservice.CreateScheduleResponse{}
	s.service.EXPECT().CreateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResp, nil).Times(1)

	scheduleHandle, err := s.client.ScheduleClient().Create(context.Background(), options)
	s.Nil(err)

	// Mock DescribeSchedule (called internally by Update)
	describeResp := newDescribeResponseWithPriority(&commonpb.Priority{
		PriorityKey:    2,
		FairnessKey:    "test-fairness-key",
		FairnessWeight: 1.5,
	})
	s.service.EXPECT().DescribeSchedule(gomock.Any(), gomock.Any(), gomock.Any()).Return(describeResp, nil).Times(1)

	// Mock UpdateSchedule and verify the priority is re-serialized correctly
	updatedPriority := Priority{
		PriorityKey:    5,
		FairnessKey:    "updated-fairness-key",
		FairnessWeight: 2.0,
	}
	s.service.EXPECT().UpdateSchedule(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(_ interface{}, req *workflowservice.UpdateScheduleRequest, _ ...interface{}) {
			// Verify the updated priority is serialized in the UpdateScheduleRequest
			action := req.Schedule.Action.GetStartWorkflow()
			s.NotNil(action)
			s.Equal(int32(5), action.Priority.PriorityKey)
			s.Equal("updated-fairness-key", action.Priority.FairnessKey)
			s.Equal(float32(2.0), action.Priority.FairnessWeight)
		}).
		Return(&workflowservice.UpdateScheduleResponse{}, nil).
		Times(1)

	// Perform Update: read the described priority, modify it, and send back
	err = scheduleHandle.Update(context.Background(), ScheduleUpdateOptions{
		DoUpdate: func(input ScheduleUpdateInput) (*ScheduleUpdate, error) {
			// Verify the describe deserialized priority correctly
			action := input.Description.Schedule.Action.(*ScheduleWorkflowAction)
			s.Equal(priority, action.Priority)

			// Modify the priority
			action.Priority = updatedPriority
			return &ScheduleUpdate{Schedule: &input.Description.Schedule}, nil
		},
	})
	s.Nil(err)
}

