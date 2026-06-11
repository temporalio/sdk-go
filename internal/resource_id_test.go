package internal

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/metrics"
)

// TestResourceIDImplementation tests that our actual implementation code
// correctly populates resource_id fields in various request types
func TestResourceIDImplementation(t *testing.T) {
	t.Run("ActivityHeartbeat", func(t *testing.T) {
		testActivityHeartbeatResourceID(t)
	})
	t.Run("WorkflowTaskRequests", func(t *testing.T) {
		testWorkflowTaskResourceID(t)
	})
	t.Run("ActivityTaskRequests", func(t *testing.T) {
		testActivityTaskRequestsResourceID(t)
	})
	t.Run("BatchOperationRequest", func(t *testing.T) {
		testExecuteMultiOperationResourceID(t)
	})
	t.Run("WorkerHeartbeatRequest", func(t *testing.T) {
		testRecordWorkerHeartbeatResourceID(t)
	})
}

func TestGetActivityResourceId_BothEmpty(t *testing.T) {
	result := getActivityResourceId("", "")
	assert.Empty(t, result, "Expected empty resource ID when both workflowId and activityId are empty")
}

// Test activity heartbeat resource_id population using actual SDK code path
func testActivityHeartbeatResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		createActivityEnv  func(ServiceInvoker) *activityEnvironment
		expectedResourceID string
	}{
		{
			name: "WithWorkflowExecution",
			createActivityEnv: func(invoker ServiceInvoker) *activityEnvironment {
				return &activityEnvironment{
					serviceInvoker: invoker,
					workflowExecution: WorkflowExecution{
						ID:    "test-workflow-123",
						RunID: "test-run-456",
					},
					logger: getLogger(),
				}
			},
			expectedResourceID: "workflow:test-workflow-123",
		},
		{
			name: "StandaloneActivity",
			createActivityEnv: func(invoker ServiceInvoker) *activityEnvironment {
				return &activityEnvironment{
					serviceInvoker: invoker,
					activityID:     "standalone-activity-999",
					logger:         getLogger(),
				}
			},
			expectedResourceID: "activity:standalone-activity-999",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test setup inside the loop for clean isolation
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			var capturedRequest *workflowservice.RecordActivityTaskHeartbeatRequest
			service.EXPECT().
				RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).
				Times(1)

			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			invoker := newServiceInvoker([]byte("test-token"), "test-identity", service, metrics.NopHandler, cancel,
				1*time.Second, make(chan struct{}), "test-namespace", &atomic.Bool{})

			// Only the activity environment creation varies
			activityCtx, _ := newActivityContext(ctx, nil, tc.createActivityEnv(invoker))

			// Call the actual SDK function
			RecordActivityHeartbeat(activityCtx, "test-heartbeat-details")

			// Validate the captured request
			require.NotNil(t, capturedRequest)
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId)
		})
	}
}

// Test workflow task request resource_id population using actual SDK code paths
func testWorkflowTaskResourceID(t *testing.T) {
	t.Run("WorkflowTaskCompleted", func(t *testing.T) {
		testWorkflowTaskCompletedResourceID(t)
	})
	t.Run("WorkflowTaskFailed", func(t *testing.T) {
		testWorkflowTaskFailedResourceID(t)
	})
}

// Test RespondWorkflowTaskCompletedRequest resource_id field (resource_id = 18)
// Expected: Workflow ID from original task
func testWorkflowTaskCompletedResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		runID              string
		expectedResourceID string
	}{
		{
			name:               "StandardWorkflowTask",
			workflowID:         "test-workflow-completed-123",
			runID:              "test-run-completed-456",
			expectedResourceID: "workflow:test-workflow-completed-123",
		},
		{
			name:               "DifferentWorkflowID",
			workflowID:         "another-workflow-789",
			runID:              "another-run-999",
			expectedResourceID: "workflow:another-workflow-789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			var capturedRequest *workflowservice.RespondWorkflowTaskCompletedRequest
			service.EXPECT().
				RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.RespondWorkflowTaskCompletedResponse{}, nil).
				Times(1)

			// Create a simple workflow task with basic history
			task := createTestWorkflowTask(tc.workflowID, tc.runID)

			// Create workflow task handler
			params := workerExecutionParameters{
				Namespace: "test-namespace",
				Identity:  "test-identity",
				cache:     NewWorkerCache(),
			}
			ensureRequiredParams(&params)
			registry := newRegistry()
			registry.RegisterWorkflowWithOptions(
				helloWorldWorkflowFunc,
				RegisterWorkflowOptions{Name: "HelloWorld_Workflow"},
			)

			handler := newWorkflowTaskHandler(params, nil, registry)

			// Process the workflow task through the poller to trigger the completion
			poller := newWorkflowTaskProcessor(handler, handler, service, params, uuid.NewString())

			wt := &workflowTask{task: task}
			err := poller.processWorkflowTask(wt)

			// We expect the task to complete successfully since workflow is registered
			require.NoError(t, err)

			// Validate the captured request
			require.NotNil(t, capturedRequest)
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId)
		})
	}
}

// Test RespondWorkflowTaskFailedRequest resource_id field (resource_id = 11)
// Expected: Workflow ID from original task
func testWorkflowTaskFailedResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		runID              string
		expectedResourceID string
		failureCause       enumspb.WorkflowTaskFailedCause
	}{
		{
			name:               "UnknownWorkflowType",
			workflowID:         "test-workflow-failed-123",
			runID:              "test-run-failed-456",
			expectedResourceID: "workflow:test-workflow-failed-123",
			failureCause:       enumspb.WORKFLOW_TASK_FAILED_CAUSE_WORKFLOW_WORKER_UNHANDLED_FAILURE,
		},
		{
			name:               "NonDeterministicError",
			workflowID:         "non-deterministic-workflow-789",
			runID:              "non-deterministic-run-999",
			expectedResourceID: "workflow:non-deterministic-workflow-789",
			failureCause:       enumspb.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			var capturedRequest *workflowservice.RespondWorkflowTaskFailedRequest
			service.EXPECT().
				RespondWorkflowTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.RespondWorkflowTaskFailedRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.RespondWorkflowTaskFailedResponse{}, nil).
				Times(1)

			// Create a workflow task that will fail (unregistered workflow type)
			task := createTestWorkflowTaskWithType(tc.workflowID, tc.runID, "UnregisteredWorkflow")

			// Create workflow task handler without registering the workflow
			params := workerExecutionParameters{
				Namespace: "test-namespace",
				Identity:  "test-identity",
				cache:     NewWorkerCache(),
			}
			ensureRequiredParams(&params)
			registry := newRegistry() // Empty registry to cause failure

			handler := newWorkflowTaskHandler(params, nil, registry)

			// Process the workflow task through the poller to trigger the failure
			poller := newWorkflowTaskProcessor(handler, handler, service, params, uuid.NewString())

			wt := &workflowTask{task: task}
			err := poller.processWorkflowTask(wt)

			// Task processing succeeds but should trigger failure request due to unregistered workflow
			require.NoError(t, err)

			// Validate the captured request
			require.NotNil(t, capturedRequest)
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId)
		})
	}
}

// Test activity task request resource_id population using actual SDK code paths
func testActivityTaskRequestsResourceID(t *testing.T) {
	t.Run("ActivityTaskHeartbeatById", func(t *testing.T) {
		testActivityTaskHeartbeatByIdResourceID(t)
	})
	t.Run("ConvertActivityResultValidation", func(t *testing.T) {
		testConvertActivityResultValidation(t)
	})
	t.Run("ConvertActivityResultByIDValidation", func(t *testing.T) {
		testConvertActivityResultByIDValidation(t)
	})
}

// Test convertActivityResultToRespondRequest validation
// This tests all 4 activity task requests that use this function:
// - RespondActivityTaskCompletedRequest (resource_id = 8)
// - RespondActivityTaskFailedRequest (resource_id = 9)
// - RespondActivityTaskCanceledRequest (resource_id = 8)
// - RecordActivityTaskHeartbeatRequest (resource_id = 5)
func testConvertActivityResultValidation(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		activityID         string
		expectedResourceID string
		testType           string
		simulateError      error
		description        string
	}{
		{
			name:               "CompletedWithWorkflow",
			workflowID:         "test-workflow-completed-123",
			activityID:         "test-activity-456",
			expectedResourceID: "workflow:test-workflow-completed-123",
			testType:           "completed",
			simulateError:      nil,
			description:        "RespondActivityTaskCompletedRequest should use workflow ID when present",
		},
		{
			name:               "CompletedStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-completed-789",
			expectedResourceID: "activity:standalone-activity-completed-789",
			testType:           "completed",
			simulateError:      nil,
			description:        "RespondActivityTaskCompletedRequest should use activity ID for standalone activities",
		},
		{
			name:               "FailedWithWorkflow",
			workflowID:         "test-workflow-failed-123",
			activityID:         "test-activity-failed-456",
			expectedResourceID: "workflow:test-workflow-failed-123",
			testType:           "failed",
			simulateError:      errors.New("activity failed"),
			description:        "RespondActivityTaskFailedRequest should use workflow ID when present",
		},
		{
			name:               "FailedStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-failed-789",
			expectedResourceID: "activity:standalone-activity-failed-789",
			testType:           "failed",
			simulateError:      errors.New("standalone activity failed"),
			description:        "RespondActivityTaskFailedRequest should use activity ID for standalone activities",
		},
		{
			name:               "CanceledWithWorkflow",
			workflowID:         "test-workflow-canceled-123",
			activityID:         "test-activity-canceled-456",
			expectedResourceID: "workflow:test-workflow-canceled-123",
			testType:           "canceled",
			simulateError:      NewCanceledError(),
			description:        "RespondActivityTaskCanceledRequest should use workflow ID when present",
		},
		{
			name:               "CanceledStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-canceled-789",
			expectedResourceID: "activity:standalone-activity-canceled-789",
			testType:           "canceled",
			simulateError:      NewCanceledError(),
			description:        "RespondActivityTaskCanceledRequest should use activity ID for standalone activities",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call the conversion function directly to test resource ID population
			result := convertActivityResultToRespondRequest(
				"test-identity",
				[]byte("test-task-token"),
				nil, // result payloads
				tc.simulateError,
				converter.GetDefaultDataConverter(), // data converter
				GetDefaultFailureConverter(),        // failure converter
				"test-namespace",
				true, // cancel allowed
				nil,  // version stamp
				nil,  // deployment
				nil,  // worker deployment options
				tc.workflowID,
				tc.activityID,
			)

			// Validate the result based on the test type
			switch tc.testType {
			case "completed":
				if tc.simulateError == nil {
					completedRequest, ok := result.(*workflowservice.RespondActivityTaskCompletedRequest)
					require.True(t, ok, "Result should be a RespondActivityTaskCompletedRequest")
					assert.Equal(t, tc.expectedResourceID, completedRequest.ResourceId, tc.description)
				}
			case "failed":
				if tc.simulateError != nil && !errors.Is(tc.simulateError, context.Canceled) {
					var canceledErr *CanceledError
					if !errors.As(tc.simulateError, &canceledErr) {
						failedRequest, ok := result.(*workflowservice.RespondActivityTaskFailedRequest)
						require.True(t, ok, "Result should be a RespondActivityTaskFailedRequest")
						assert.Equal(t, tc.expectedResourceID, failedRequest.ResourceId, tc.description)
					}
				}
			case "canceled":
				var canceledErr *CanceledError
				if errors.As(tc.simulateError, &canceledErr) {
					canceledRequest, ok := result.(*workflowservice.RespondActivityTaskCanceledRequest)
					require.True(t, ok, "Result should be a RespondActivityTaskCanceledRequest")
					assert.Equal(t, tc.expectedResourceID, canceledRequest.ResourceId, tc.description)
				}
			}
		})
	}
}

// Test convertActivityResultToRespondRequestByID validation
// This tests all 4 ByID activity task requests that use this function:
// - RespondActivityTaskCompletedByIdRequest (resource_id = 7)
// - RespondActivityTaskFailedByIdRequest (resource_id = 8)
// - RespondActivityTaskCanceledByIdRequest (resource_id = 8)
// - RecordActivityTaskHeartbeatByIdRequest (resource_id = 7)
func testConvertActivityResultByIDValidation(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		activityID         string
		expectedResourceID string
		testType           string
		simulateError      error
		description        string
	}{
		{
			name:               "CompletedByIDWithWorkflow",
			workflowID:         "test-workflow-completed-by-id-123",
			activityID:         "test-activity-by-id-456",
			expectedResourceID: "workflow:test-workflow-completed-by-id-123",
			testType:           "completed",
			simulateError:      nil,
			description:        "RespondActivityTaskCompletedByIdRequest should use workflow ID when present",
		},
		{
			name:               "CompletedByIDStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-completed-by-id-789",
			expectedResourceID: "activity:standalone-activity-completed-by-id-789",
			testType:           "completed",
			simulateError:      nil,
			description:        "RespondActivityTaskCompletedByIdRequest should use activity ID for standalone activities",
		},
		{
			name:               "FailedByIDWithWorkflow",
			workflowID:         "test-workflow-failed-by-id-123",
			activityID:         "test-activity-failed-by-id-456",
			expectedResourceID: "workflow:test-workflow-failed-by-id-123",
			testType:           "failed",
			simulateError:      errors.New("activity failed by ID"),
			description:        "RespondActivityTaskFailedByIdRequest should use workflow ID when present",
		},
		{
			name:               "FailedByIDStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-failed-by-id-789",
			expectedResourceID: "activity:standalone-activity-failed-by-id-789",
			testType:           "failed",
			simulateError:      errors.New("standalone activity failed by ID"),
			description:        "RespondActivityTaskFailedByIdRequest should use activity ID for standalone activities",
		},
		{
			name:               "CanceledByIDWithWorkflow",
			workflowID:         "test-workflow-canceled-by-id-123",
			activityID:         "test-activity-canceled-by-id-456",
			expectedResourceID: "workflow:test-workflow-canceled-by-id-123",
			testType:           "canceled",
			simulateError:      NewCanceledError(),
			description:        "RespondActivityTaskCanceledByIdRequest should use workflow ID when present",
		},
		{
			name:               "CanceledByIDStandalone",
			workflowID:         "",
			activityID:         "standalone-activity-canceled-by-id-789",
			expectedResourceID: "activity:standalone-activity-canceled-by-id-789",
			testType:           "canceled",
			simulateError:      NewCanceledError(),
			description:        "RespondActivityTaskCanceledByIdRequest should use activity ID for standalone activities",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call the conversion function directly to test resource ID population
			result := convertActivityResultToRespondRequestByID(
				"test-identity",
				"test-namespace",
				tc.workflowID,
				"test-run-id",
				tc.activityID,
				nil, // result payloads
				tc.simulateError,
				converter.GetDefaultDataConverter(), // data converter
				GetDefaultFailureConverter(),        // failure converter
				true,                                // cancel allowed
			)

			// Validate the result based on the test type
			switch tc.testType {
			case "completed":
				if tc.simulateError == nil {
					completedRequest, ok := result.(*workflowservice.RespondActivityTaskCompletedByIdRequest)
					require.True(t, ok, "Result should be a RespondActivityTaskCompletedByIdRequest")
					assert.Equal(t, tc.expectedResourceID, completedRequest.ResourceId, tc.description)
				}
			case "failed":
				if tc.simulateError != nil && !errors.Is(tc.simulateError, context.Canceled) {
					var canceledErr *CanceledError
					if !errors.As(tc.simulateError, &canceledErr) {
						failedRequest, ok := result.(*workflowservice.RespondActivityTaskFailedByIdRequest)
						require.True(t, ok, "Result should be a RespondActivityTaskFailedByIdRequest")
						assert.Equal(t, tc.expectedResourceID, failedRequest.ResourceId, tc.description)
					}
				}
			case "canceled":
				var canceledErr *CanceledError
				if errors.As(tc.simulateError, &canceledErr) {
					canceledRequest, ok := result.(*workflowservice.RespondActivityTaskCanceledByIdRequest)
					require.True(t, ok, "Result should be a RespondActivityTaskCanceledByIdRequest")
					assert.Equal(t, tc.expectedResourceID, canceledRequest.ResourceId, tc.description)
				}
			}
		})
	}
}

// Helper functions for creating test workflow tasks

func createTestWorkflowTask(workflowID, runID string) *workflowservice.PollWorkflowTaskQueueResponse {
	return createTestWorkflowTaskWithType(workflowID, runID, "HelloWorld_Workflow")
}

func createTestWorkflowTaskWithType(workflowID, runID, workflowType string) *workflowservice.PollWorkflowTaskQueueResponse {
	events := []*historypb.HistoryEvent{
		{
			EventId:   1,
			EventTime: nil,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: workflowType},
					TaskQueue:    &taskqueuepb.TaskQueue{Name: "test-task-queue"},
				},
			},
		},
		{
			EventId:   2,
			EventTime: nil,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{
				WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{
					TaskQueue: &taskqueuepb.TaskQueue{Name: "test-task-queue"},
				},
			},
		},
		{
			EventId:   3,
			EventTime: nil,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
				WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{
					Identity: "test-identity",
				},
			},
		},
	}

	return &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken: []byte("test-task-token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: workflowID,
			RunId:      runID,
		},
		WorkflowType: &commonpb.WorkflowType{Name: workflowType},
		History:      &historypb.History{Events: events},
		Attempt:      1,
	}
}

// Test RecordActivityTaskHeartbeatByIdRequest resource_id field (resource_id = 7)
// Expected: Workflow ID or activity ID for standalone activities
func testActivityTaskHeartbeatByIdResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		activityID         string
		expectedResourceID string
		description        string
	}{
		{
			name:               "WithWorkflowExecution",
			workflowID:         "test-workflow-heartbeat-by-id-123",
			activityID:         "test-activity-heartbeat-by-id-456",
			expectedResourceID: "workflow:test-workflow-heartbeat-by-id-123",
			description:        "Should use workflow ID when present",
		},
		{
			name:               "StandaloneActivity",
			workflowID:         "",
			activityID:         "standalone-activity-heartbeat-by-id-789",
			expectedResourceID: "activity:standalone-activity-heartbeat-by-id-789",
			description:        "Should use activity ID for standalone activities",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			// Mock GetSystemInfo which is called during client initialization
			service.EXPECT().
				GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetSystemInfoResponse{}, nil).
				AnyTimes()

			var capturedRequest *workflowservice.RecordActivityTaskHeartbeatByIdRequest
			service.EXPECT().
				RecordActivityTaskHeartbeatById(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.RecordActivityTaskHeartbeatByIdRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.RecordActivityTaskHeartbeatByIdResponse{}, nil).
				Times(1)

			// Create workflow client and call heartbeat by ID
			client := NewServiceClient(service, nil, ClientOptions{Namespace: "test-namespace"})

			ctx := context.Background()
			err := client.RecordActivityHeartbeatByID(ctx, "test-namespace", tc.workflowID, "", tc.activityID, "heartbeat-details")

			// Validate call succeeded
			require.NoError(t, err)

			// Validate the captured request
			require.NotNil(t, capturedRequest, "Request should have been captured")
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId, tc.description)
		})
	}
}

// Test ExecuteMultiOperationRequest resource_id field (resource_id = 3)
// Expected: Should match operations[0].start_workflow.workflow_id
func testExecuteMultiOperationResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		workflowID         string
		expectedResourceID string
	}{
		{
			name:               "StartUpdateWorkflow",
			workflowID:         "test-workflow-batch-123",
			expectedResourceID: "workflow:test-workflow-batch-123",
		},
		{
			name:               "MultiOpWithDifferentID",
			workflowID:         "batch-operation-workflow-456",
			expectedResourceID: "workflow:batch-operation-workflow-456",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			// Mock GetSystemInfo which gets called during client operations
			service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

			var capturedRequest *workflowservice.ExecuteMultiOperationRequest
			service.EXPECT().
				ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.ExecuteMultiOperationRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.ExecuteMultiOperationResponse{
					Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
						{
							Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
								StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{
									RunId: "test-run-id",
								},
							},
						},
						{
							Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{
								UpdateWorkflow: &workflowservice.UpdateWorkflowExecutionResponse{
									Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
								},
							},
						},
					},
				}, nil).
				Times(1)

			// Create a client to trigger ExecuteMultiOperation via UpdateWithStartWorkflow
			client := NewServiceClient(service, nil, ClientOptions{
				Namespace: "test-namespace",
			})

			// Create start operation with workflow options
			startOp := client.NewWithStartWorkflowOperation(
				StartWorkflowOptions{
					ID:                       tc.workflowID,
					TaskQueue:                "test-task-queue",
					WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
				},
				"TestWorkflow",
			)

			// Execute the update with start workflow operation
			ctx := context.Background()
			_, err := client.UpdateWithStartWorkflow(ctx, UpdateWithStartWorkflowOptions{
				StartWorkflowOperation: startOp,
				UpdateOptions: UpdateWorkflowOptions{
					UpdateName:   "TestUpdate",
					Args:         []any{},
					WaitForStage: WorkflowUpdateStageAccepted,
				},
			})

			// The operation should succeed
			require.NoError(t, err)

			// Validate the captured request
			require.NotNil(t, capturedRequest, "ExecuteMultiOperationRequest should have been captured")
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId,
				"ResourceId should match the workflow ID from the start operation")

			// Additional validation: ensure we have the expected operations
			require.Len(t, capturedRequest.Operations, 2, "Should have start and update operations")

			// Verify the first operation is a start workflow operation with the expected workflow ID
			startWorkflowOp := capturedRequest.Operations[0].GetStartWorkflow()
			require.NotNil(t, startWorkflowOp, "First operation should be StartWorkflow")
			assert.Equal(t, tc.workflowID, startWorkflowOp.WorkflowId,
				"Start workflow operation should have the expected workflow ID")
		})
	}
}

// Test RecordWorkerHeartbeatRequest resource_id field (resource_id = 4)
// Expected: Contains the worker grouping key
func testRecordWorkerHeartbeatResourceID(t *testing.T) {
	testCases := []struct {
		name               string
		groupingKey        string
		expectedResourceID string
	}{
		{
			name:               "WorkerHeartbeat",
			groupingKey:        "test-worker-grouping-key-123",
			expectedResourceID: "worker:test-worker-grouping-key-123",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			service := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)

			// Mock GetSystemInfo which gets called during client operations
			service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

			var capturedRequest *workflowservice.RecordWorkerHeartbeatRequest
			service.EXPECT().
				RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(ctx context.Context, req *workflowservice.RecordWorkerHeartbeatRequest, opts ...interface{}) {
					capturedRequest = req
				}).
				Return(&workflowservice.RecordWorkerHeartbeatResponse{}, nil).
				Times(1)

			// Create a client with a specific workerGroupingKey
			wfClient := NewServiceClient(service, nil, ClientOptions{
				Namespace: "test-namespace",
			})

			// Set the workerGroupingKey to our test value
			wfClient.workerGroupingKey = tc.groupingKey

			// Create a sharedNamespaceWorker and call sendHeartbeats
			heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
			defer heartbeatCancel()

			hw := &sharedNamespaceWorker{
				client:          wfClient,
				namespace:       "test-namespace",
				heartbeatCtx:    heartbeatCtx,
				heartbeatCancel: heartbeatCancel,
				callbacks: map[string]func() *workerpb.WorkerHeartbeat{
					"test-worker": func() *workerpb.WorkerHeartbeat {
						return &workerpb.WorkerHeartbeat{
							WorkerIdentity: "test-worker-identity",
						}
					},
				},
			}

			// Call sendHeartbeats which should construct and send the request
			err := hw.sendHeartbeats()

			// The operation should succeed
			require.NoError(t, err)

			// Validate the captured request
			require.NotNil(t, capturedRequest, "RecordWorkerHeartbeatRequest should have been captured")
			assert.Equal(t, tc.expectedResourceID, capturedRequest.ResourceId,
				"ResourceId should match the worker grouping key")

			// Additional validation
			assert.Equal(t, "test-namespace", capturedRequest.Namespace,
				"Namespace should be set correctly")
			require.Len(t, capturedRequest.WorkerHeartbeat, 1,
				"Should have one worker heartbeat")
			assert.Equal(t, "test-worker-identity", capturedRequest.WorkerHeartbeat[0].WorkerIdentity,
				"Worker identity should be set correctly")
		})
	}
}
