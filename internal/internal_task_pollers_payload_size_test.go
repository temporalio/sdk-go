package internal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/extstore"
	"go.temporal.io/sdk/internal/payloadlimits"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func makeTestPayloads(size int) *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(size)}}
}

func payloadSizeErrorForTest() error {
	return payloadlimits.NewError(&payloadlimits.Violation{Severity: payloadlimits.SeverityError})
}

// The error's type name is the type of the task failure that replaces a rejected request, which users
// can match in retry policies.
func TestPayloadSizeErrorFailureType(t *testing.T) {
	failure := GetDefaultFailureConverter().ErrorToFailure(payloadSizeErrorForTest())
	require.Equal(t, "payloadSizeError", failure.GetApplicationFailureInfo().GetType())
}

func TestActivityFailureForPayloadLimit(t *testing.T) {
	violation := payloadSizeErrorForTest()
	converter := GetDefaultFailureConverter()

	t.Run("replaces a completion", func(t *testing.T) {
		failed := activityFailureForPayloadLimit(&workflowservice.RespondActivityTaskCompletedRequest{
			TaskToken: []byte("token"), Identity: "id", Namespace: "ns", ResourceId: "res", Result: makeTestPayloads(10),
		}, violation, converter)
		require.NotNil(t, failed)
		require.Equal(t, []byte("token"), failed.GetTaskToken())
		require.Equal(t, "id", failed.GetIdentity())
		require.Equal(t, "ns", failed.GetNamespace())
		require.Equal(t, "res", failed.GetResourceId())
		require.Equal(t, violation.Error(), failed.GetFailure().GetMessage())
		require.Equal(t, enumspb.ACTIVITY_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE, failed.GetCause())
	})

	t.Run("replaces a cancellation", func(t *testing.T) {
		failed := activityFailureForPayloadLimit(&workflowservice.RespondActivityTaskCanceledRequest{
			TaskToken: []byte("token"), Details: makeTestPayloads(10),
		}, violation, converter)
		require.NotNil(t, failed)
		require.Equal(t, []byte("token"), failed.GetTaskToken())
		require.Equal(t, enumspb.ACTIVITY_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE, failed.GetCause())
	})

	t.Run("ignores other errors", func(t *testing.T) {
		require.Nil(t, activityFailureForPayloadLimit(&workflowservice.RespondActivityTaskCompletedRequest{}, errors.New("other"), converter))
	})
}

func newPayloadSizeTestProcessor(t *testing.T, service workflowservice.WorkflowServiceClient, errorLimit int64) *workflowTaskProcessor {
	t.Helper()
	errorLimits := &atomic.Pointer[payloadlimits.Limits]{}
	errorLimits.Store(&payloadlimits.Limits{BlobError: errorLimit, MemoError: errorLimit})
	params := workerExecutionParameters{
		cache:              newTestWorkerCache(t),
		Namespace:          "test-namespace",
		payloadErrorLimits: errorLimits,
	}
	ensureRequiredParams(&params)
	taskHandler := newWorkflowTaskHandler(params, nil, newRegistry())
	return newWorkflowTaskProcessor(taskHandler, taskHandler, service, params, "")
}

func payloadSizeTestTask(query *querypb.WorkflowQuery) *workflowservice.PollWorkflowTaskQueueResponse {
	return &workflowservice.PollWorkflowTaskQueueResponse{
		TaskToken:         []byte("token"),
		Attempt:           1,
		WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: "workflow-id", RunId: "run-id"},
		WorkflowType:      &commonpb.WorkflowType{Name: "workflow-type"},
		Query:             query,
	}
}

func TestWorkflowTaskCompletionOverPayloadErrorLimitFailsTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	service.EXPECT().RespondWorkflowTaskCompleted(gomock.Any(), gomock.Any()).
		Return(nil, payloadSizeErrorForTest())
	service.EXPECT().RespondWorkflowTaskFailed(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondWorkflowTaskFailedRequest, _ ...grpc.CallOption) (*workflowservice.RespondWorkflowTaskFailedResponse, error) {
			require.Equal(t, enumspb.WORKFLOW_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE, req.GetCause())
			require.Equal(t, "payloadSizeError", req.GetFailure().GetApplicationFailureInfo().GetType())
			return &workflowservice.RespondWorkflowTaskFailedResponse{}, nil
		})
	processor := newPayloadSizeTestProcessor(t, service, 1000)

	response, err := processor.RespondTaskCompletedWithMetrics(
		&workflowTaskCompletion{rawRequest: &workflowservice.RespondWorkflowTaskCompletedRequest{TaskToken: []byte("token")}},
		nil,
		payloadSizeTestTask(nil),
		time.Now(),
		&workflowTaskStorageMetrics{},
		&WorkflowInfo{},
	)

	require.NoError(t, err)
	require.Nil(t, response)
}

func TestPaginatedCompletionOverPayloadErrorLimitSendsNoPages(t *testing.T) {
	ctrl := gomock.NewController(t)
	// No RespondWorkflowTaskCompleted expectation: sending any page fails the test.
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	processor := newPayloadSizeTestProcessor(t, service, 2_500_000)
	processor.workflowTaskCompletionPagination = &workflowTaskCompletionPaginationConfig{}
	processor.workflowTaskCompletionPagination.enabled.Store(true)
	// The first two commands fill their own pages; only the last, on the final page, is over the limit.
	request := paginationTestRequest([]*commandpb.Command{
		paginationTestCommand(2_000_000),
		paginationTestCommand(2_000_000),
		paginationTestCommand(3_000_000),
	})

	_, err := processor.respondWorkflowTaskCompleted(t.Context(), t.Context(), request, payloadSizeTestTask(nil))

	require.True(t, payloadlimits.IsError(err))
}

func TestLegacyQueryResultOverPayloadErrorLimitFailsQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	service.EXPECT().RespondQueryTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondQueryTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondQueryTaskCompletedResponse, error) {
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, req.GetCompletedType())
			require.Nil(t, req.GetQueryResult())
			return &workflowservice.RespondQueryTaskCompletedResponse{}, nil
		})
	processor := newPayloadSizeTestProcessor(t, service, 500)

	_, err := processor.RespondTaskCompletedWithMetrics(
		&workflowTaskCompletion{rawRequest: &workflowservice.RespondQueryTaskCompletedRequest{
			TaskToken:   []byte("token"),
			QueryResult: makeTestPayloads(2000),
		}},
		nil,
		payloadSizeTestTask(&querypb.WorkflowQuery{QueryType: "status"}),
		time.Now(),
		&workflowTaskStorageMetrics{},
		&WorkflowInfo{},
	)

	require.NoError(t, err)
}

func TestQueryResultOffloadedToExternalStorageIsMeasuredAsItsReference(t *testing.T) {
	ctrl := gomock.NewController(t)
	service := workflowservicemock.NewMockWorkflowServiceClient(ctrl)
	service.EXPECT().RespondQueryTaskCompleted(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *workflowservice.RespondQueryTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondQueryTaskCompletedResponse, error) {
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_UNSPECIFIED, req.GetCompletedType())
			require.True(t, extstore.IsStorageReference(req.GetQueryResult().GetPayloads()[0]))
			return &workflowservice.RespondQueryTaskCompletedResponse{}, nil
		})
	processor := newPayloadSizeTestProcessor(t, service, 500)
	driver := newCountingStorageDriver()
	params, err := extstore.ExternalStorageToParams(extstore.ExternalStorage{
		Drivers:              []extstore.StorageDriver{driver},
		PayloadSizeThreshold: 10,
	})
	require.NoError(t, err)
	processor.outboundPayloadVisitor = extstore.NewExternalStorageVisitor(params)

	_, err = processor.RespondTaskCompletedWithMetrics(
		&workflowTaskCompletion{rawRequest: &workflowservice.RespondQueryTaskCompletedRequest{
			TaskToken:   []byte("token"),
			QueryResult: makeTestPayloads(2000),
		}},
		nil,
		payloadSizeTestTask(&querypb.WorkflowQuery{QueryType: "status"}),
		time.Now(),
		&workflowTaskStorageMetrics{},
		&WorkflowInfo{},
	)

	require.NoError(t, err)
	require.Equal(t, 1, driver.storeCalls())
}

// countingStorageDriver is a minimal in-memory extstore.StorageDriver used to confirm that a payload
// is actually offloaded rather than merely not erroring.
type countingStorageDriver struct {
	mu         sync.Mutex
	storeCount int
	data       map[string]*commonpb.Payload
}

func newCountingStorageDriver() *countingStorageDriver {
	return &countingStorageDriver{data: map[string]*commonpb.Payload{}}
}

func (d *countingStorageDriver) Name() string { return "counting" }
func (d *countingStorageDriver) Type() string { return "counting" }

func (d *countingStorageDriver) Store(_ extstore.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]extstore.StorageDriverClaim, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeCount++
	claims := make([]extstore.StorageDriverClaim, len(payloads))
	for i, p := range payloads {
		key := fmt.Sprintf("k%d", len(d.data))
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = extstore.StorageDriverClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *countingStorageDriver) Retrieve(_ extstore.StorageDriverRetrieveContext, claims []extstore.StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		out[i] = d.data[c.ClaimData["key"]]
	}
	return out, nil
}

func (d *countingStorageDriver) storeCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.storeCount
}
