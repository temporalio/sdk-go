package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/retry"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func paginationTestCommand(payloadSize int) *commandpb.Command {
	return &commandpb.Command{
		CommandType: enumspb.COMMAND_TYPE_RECORD_MARKER,
		Attributes: &commandpb.Command_RecordMarkerCommandAttributes{
			RecordMarkerCommandAttributes: &commandpb.RecordMarkerCommandAttributes{
				MarkerName: "test",
				Details: map[string]*commonpb.Payloads{
					"data": {Payloads: []*commonpb.Payload{{Data: make([]byte, payloadSize)}}},
				},
			},
		},
	}
}

func paginationTestRequest(commands []*commandpb.Command) *workflowservice.RespondWorkflowTaskCompletedRequest {
	return &workflowservice.RespondWorkflowTaskCompletedRequest{
		TaskToken:  []byte("task-token"),
		Namespace:  "namespace",
		Identity:   "identity",
		ResourceId: "workflow:workflow-id",
		Commands:   commands,
	}
}

func TestPaginateWorkflowTaskCompletion_FitsInSinglePage(t *testing.T) {
	request := paginationTestRequest([]*commandpb.Command{paginationTestCommand(16)})
	intermediate, final := paginateWorkflowTaskCompletion(request, 4096)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
	require.False(t, final.IntermediatePage)
	require.EqualValues(t, 0, final.PageNumber)
}

func TestPaginateWorkflowTaskCompletion_SplitsCommandsAcrossPages(t *testing.T) {
	const maxPageBytes = 1024
	const commandCount = 6
	var commands []*commandpb.Command
	for i := 0; i < commandCount; i++ {
		commands = append(commands, paginationTestCommand(400))
	}
	request := paginationTestRequest(commands)
	require.Greater(t, proto.Size(request), maxPageBytes)

	intermediate, final := paginateWorkflowTaskCompletion(request, maxPageBytes)
	require.NotEmpty(t, intermediate)

	// The final page carries no commands and is numbered after every intermediate page.
	require.False(t, final.IntermediatePage)
	require.Empty(t, final.Commands)
	require.EqualValues(t, len(intermediate), final.PageNumber)
	require.LessOrEqual(t, proto.Size(final), maxPageBytes)

	totalCommands := 0
	for i, page := range intermediate {
		require.True(t, page.IntermediatePage)
		require.EqualValues(t, i, page.PageNumber)
		require.Equal(t, []byte("task-token"), page.TaskToken)
		require.Equal(t, request.ResourceId, page.ResourceId)
		require.LessOrEqual(t, proto.Size(page), maxPageBytes, "intermediate page %d over limit", i)
		totalCommands += len(page.Commands)
	}
	// Every command is preserved exactly once across the intermediate pages.
	require.Equal(t, commandCount, totalCommands)
}

func TestPaginateWorkflowTaskCompletion_SingleCommandLargerThanPageIsNotSplit(t *testing.T) {
	request := paginationTestRequest([]*commandpb.Command{paginationTestCommand(4096)})
	intermediate, final := paginateWorkflowTaskCompletion(request, 1024)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
}

func TestPaginateWorkflowTaskCompletion_NoCommandsIsNotSplit(t *testing.T) {
	// Over the limit because of messages, but with no commands there is nothing to distribute.
	request := paginationTestRequest(nil)
	request.Messages = []*protocolpb.Message{{Id: string(make([]byte, 2048))}}
	intermediate, final := paginateWorkflowTaskCompletion(request, 1024)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
}

const paginationTestCommandCount = 4

// oversizedPaginationTestRequest returns a completion over maxWorkflowTaskCompletionPageBytes, so it
// is split into at least two intermediate pages.
func oversizedPaginationTestRequest() *workflowservice.RespondWorkflowTaskCompletedRequest {
	var commands []*commandpb.Command
	for range paginationTestCommandCount {
		commands = append(commands, paginationTestCommand(1024*1024))
	}
	return paginationTestRequest(commands)
}

func paginationTestTask() *workflowservice.PollWorkflowTaskQueueResponse {
	return &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken: []byte("task-token"),
		Attempt:   1,
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "workflow-id",
			RunId:      "run-id",
		},
		WorkflowType: &commonpb.WorkflowType{Name: "workflow-type"},
	}
}

func newPaginationTestProcessor(t *testing.T, service workflowservice.WorkflowServiceClient) *workflowTaskProcessor {
	params := workerExecutionParameters{
		cache:                            newTestWorkerCache(t),
		Namespace:                        "namespace",
		workflowTaskCompletionPagination: &workflowTaskCompletionPaginationConfig{},
	}
	params.workflowTaskCompletionPagination.enabled.Store(true)
	ensureRequiredParams(&params)
	taskHandler := newWorkflowTaskHandler(params, nil, newRegistry())
	return newWorkflowTaskProcessor(taskHandler, taskHandler, service, params, "")
}

// completionPageRecorder records completion requests, which the paginated path sends from
// concurrent goroutines.
type completionPageRecorder struct {
	mu   sync.Mutex
	sent []*workflowservice.RespondWorkflowTaskCompletedRequest
}

func (r *completionPageRecorder) record(request *workflowservice.RespondWorkflowTaskCompletedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, request)
}

func (r *completionPageRecorder) pages() []*workflowservice.RespondWorkflowTaskCompletedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*workflowservice.RespondWorkflowTaskCompletedRequest(nil), r.sent...)
}

// intermediateSendCounts returns how many times each intermediate page number was sent.
func (r *completionPageRecorder) intermediateSendCounts() map[int32]int {
	counts := map[int32]int{}
	for _, page := range r.pages() {
		if page.IntermediatePage {
			counts[page.PageNumber]++
		}
	}
	return counts
}

func TestRespondWorkflowTaskCompleted_SendsIntermediatePagesThenFinalPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	var recorder completionPageRecorder
	finalResponse := &workflowservice.RespondWorkflowTaskCompletedResponse{}
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			recorder.record(req)
			if req.IntermediatePage {
				return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
			}
			return finalResponse, nil
		}).AnyTimes()

	request := oversizedPaginationTestRequest()
	request.Messages = []*protocolpb.Message{{Id: "message"}}
	processor := newPaginationTestProcessor(t, service)
	response, err := processor.sendTaskCompletedRequest(&workflowTaskCompletion{rawRequest: request}, paginationTestTask())
	require.NoError(t, err)
	require.Same(t, finalResponse, response)

	sent := recorder.pages()
	require.Greater(t, len(sent), 2, "expected at least two intermediate pages and a final page")
	// Intermediate pages may arrive in any order, but the final page must arrive after all of them.
	intermediate, final := sent[:len(sent)-1], sent[len(sent)-1]
	require.False(t, final.IntermediatePage)
	require.EqualValues(t, len(intermediate), final.PageNumber)
	require.Empty(t, final.Commands)
	require.Len(t, final.Messages, 1)

	var pageNumbers, expectedPageNumbers []int32
	commandCount := 0
	for i, page := range intermediate {
		require.True(t, page.IntermediatePage)
		require.Empty(t, page.Messages)
		pageNumbers = append(pageNumbers, page.PageNumber)
		expectedPageNumbers = append(expectedPageNumbers, int32(i))
		commandCount += len(page.Commands)
	}
	require.ElementsMatch(t, expectedPageNumbers, pageNumbers)
	require.Equal(t, paginationTestCommandCount, commandCount)
	for _, page := range sent {
		require.Equal(t, []byte("task-token"), page.TaskToken)
	}
}

func TestRespondWorkflowTaskCompleted_PaginationDisabledSendsSingleRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			require.False(t, req.IntermediatePage)
			require.Zero(t, req.PageNumber)
			require.Len(t, req.Commands, paginationTestCommandCount)
			return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
		})

	processor := newPaginationTestProcessor(t, service)
	processor.workflowTaskCompletionPagination.enabled.Store(false)
	_, err := processor.sendTaskCompletedRequest(
		&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()}, paginationTestTask())
	require.NoError(t, err)
}

func TestRespondWorkflowTaskCompleted_FailedPageCancelsInFlightPages(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	var finalSent atomic.Bool
	blockedPageErrs := make(chan error, paginationTestCommandCount)
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			if !req.IntermediatePage {
				finalSent.Store(true)
				return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
			}
			if req.PageNumber == 0 {
				return nil, serviceerror.NewInvalidArgument("page rejected")
			}
			// Without cancellation this page would only return at the RPC deadline.
			<-ctx.Done()
			blockedPageErrs <- ctx.Err()
			return nil, ctx.Err()
		}).AnyTimes()

	processor := newPaginationTestProcessor(t, service)
	_, err := processor.sendTaskCompletedRequest(
		&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()}, paginationTestTask())
	var invalidArgument *serviceerror.InvalidArgument
	require.ErrorAs(t, err, &invalidArgument)
	require.False(t, finalSent.Load(), "final page must not be sent after an intermediate page fails")
	close(blockedPageErrs)
	require.NotEmpty(t, blockedPageErrs)
	for blockedPageErr := range blockedPageErrs {
		require.ErrorIs(t, blockedPageErr, context.Canceled)
	}
}

func TestRespondWorkflowTaskCompleted_BufferLossResendsEveryPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	var recorder completionPageRecorder
	var finalAttempts atomic.Int32
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			recorder.record(req)
			if !req.IntermediatePage && finalAttempts.Add(1) <= 2 {
				return nil, serviceerror.NewWorkflowTaskCompletionBufferLost("buffer lost")
			}
			return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
		}).AnyTimes()

	processor := newPaginationTestProcessor(t, service)
	_, err := processor.sendTaskCompletedRequest(
		&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()}, paginationTestTask())
	require.NoError(t, err)
	require.EqualValues(t, 3, finalAttempts.Load())
	sendCounts := recorder.intermediateSendCounts()
	require.NotEmpty(t, sendCounts)
	for pageNumber, count := range sendCounts {
		require.Equal(t, 3, count, "intermediate page %d must be resent with every attempt", pageNumber)
	}
}

func TestRespondWorkflowTaskCompleted_OtherErrorsAreNotResent(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	var recorder completionPageRecorder
	var finalAttempts atomic.Int32
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			recorder.record(req)
			// Accepting a resend lets a regression fail the assertions below instead of looping forever.
			if !req.IntermediatePage && finalAttempts.Add(1) == 1 {
				// What the server returns once the task has timed out and the token is stale.
				return nil, serviceerror.NewNotFound("workflow task not found")
			}
			return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
		}).AnyTimes()

	processor := newPaginationTestProcessor(t, service)
	_, err := processor.sendTaskCompletedRequest(
		&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()}, paginationTestTask())
	var notFound *serviceerror.NotFound
	require.ErrorAs(t, err, &notFound)
	for pageNumber, count := range recorder.intermediateSendCounts() {
		require.Equal(t, 1, count, "intermediate page %d", pageNumber)
	}
}

func TestRespondWorkflowTaskCompleted_WorkerStopEndsBufferLossResends(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	stopC := make(chan struct{})
	var stopOnce sync.Once
	var finalAttempts atomic.Int32
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
			if req.IntermediatePage {
				return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
			}
			if finalAttempts.Add(1) > 1 {
				// Accepting a resend lets a regression fail the assertions below instead of looping forever.
				return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
			}
			// Stop while the first attempt is in flight, so the resend backoff observes it.
			stopOnce.Do(func() { close(stopC) })
			return nil, serviceerror.NewWorkflowTaskCompletionBufferLost("buffer lost")
		}).AnyTimes()

	processor := newPaginationTestProcessor(t, service)
	processor.stopC = stopC
	_, err := processor.sendTaskCompletedRequest(
		&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()}, paginationTestTask())
	require.True(t, retry.IsWorkflowTaskCompletionBufferLost(err), "expected buffer loss error, got %v", err)
	require.EqualValues(t, 1, finalAttempts.Load())
}

func TestRespondTaskCompletedWithMetrics_NamespaceCompletionSizeLimit(t *testing.T) {
	t.Run("paginated completion over limit fails without sending pages", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
		service.EXPECT().RespondWorkflowTaskFailed(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskFailedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskFailedResponse, error) {
				require.Equal(t, enumspb.WORKFLOW_TASK_FAILED_CAUSE_REQUEST_TOO_LARGE, req.Cause)
				require.Equal(t, []byte("task-token"), req.TaskToken)
				return &workflowservice.RespondWorkflowTaskFailedResponse{}, nil
			})

		metricsHandler := metrics.NewCapturingHandler()
		processor := newPaginationTestProcessor(t, service)
		processor.metricsHandler = metricsHandler
		processor.workflowTaskCompletionPagination.sizeLimit.Store(1024 * 1024)
		_, err := processor.RespondTaskCompletedWithMetrics(
			&workflowTaskCompletion{rawRequest: oversizedPaginationTestRequest()},
			nil,
			paginationTestTask(),
			time.Now(),
			&workflowTaskStorageMetrics{},
			nil,
		)
		require.NoError(t, err)

		var failureReasons []string
		for _, counter := range metricsHandler.Counters() {
			if counter.Name == metrics.WorkflowTaskExecutionFailureCounter {
				failureReasons = append(failureReasons, counter.Tags[metrics.FailureReasonTagName])
			}
		}
		require.Equal(t, []string{metrics.FailureReasonRequestTooLarge}, failureReasons)
	})

	t.Run("completion over limit that fits one request is sent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
		service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskCompletedResponse, error) {
				require.False(t, req.IntermediatePage)
				require.Len(t, req.Commands, 1)
				return &workflowservice.RespondWorkflowTaskCompletedResponse{}, nil
			})

		processor := newPaginationTestProcessor(t, service)
		processor.workflowTaskCompletionPagination.sizeLimit.Store(1024)
		_, err := processor.RespondTaskCompletedWithMetrics(
			&workflowTaskCompletion{rawRequest: paginationTestRequest([]*commandpb.Command{paginationTestCommand(2048)})},
			nil,
			paginationTestTask(),
			time.Now(),
			&workflowTaskStorageMetrics{},
			nil,
		)
		require.NoError(t, err)
	})
}
