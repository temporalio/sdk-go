package payloadlimits

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	querypb "go.temporal.io/api/query/v1"
	"go.temporal.io/api/workflowservice/v1"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
)

// recordingConn stands in for the gRPC connection under a conn.
type recordingConn struct {
	invoked int
}

func (c *recordingConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	c.invoked++
	return nil
}

func (c *recordingConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("not implemented")
}

func errorLimitsOf(limit int64) *atomic.Pointer[Limits] {
	limits := &atomic.Pointer[Limits]{}
	limits.Store(&Limits{BlobError: limit, MemoError: limit})
	return limits
}

func hasWarningLine(logger *ilog.MemoryLogger) bool {
	return slices.ContainsFunc(logger.Lines(), func(line string) bool {
		return strings.Contains(line, "WARN  [TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
	})
}

func TestConnWithoutErrorLimitsOnlyWarns(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	cc := &recordingConn{}
	service := workflowservice.NewWorkflowServiceClient(NewConn(cc, Limits{BlobWarn: 100}, nil, logger))

	_, err := service.StartWorkflowExecution(t.Context(), &workflowservice.StartWorkflowExecutionRequest{Input: payloads(10_000)})

	require.NoError(t, err)
	require.Equal(t, 1, cc.invoked)
	require.True(t, hasWarningLine(logger))
}

func TestConnRejectsRequestOverErrorLimit(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	cc := &recordingConn{}
	service := workflowservice.NewWorkflowServiceClient(NewConn(cc, Limits{BlobWarn: 100}, errorLimitsOf(1000), logger))

	_, err := service.RespondActivityTaskCompleted(t.Context(), &workflowservice.RespondActivityTaskCompletedRequest{Result: payloads(10_000)})

	var sizeErr payloadSizeError
	require.ErrorAs(t, err, &sizeErr)
	require.True(t, IsError(err))
	require.Equal(t, "result", sizeErr.violation.Path)
	require.Equal(t, int64(1000), sizeErr.violation.Limit)
	require.Equal(t, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.", err.Error())
	require.Zero(t, cc.invoked, "a rejected request must not be sent")
	require.Empty(t, logger.Lines(), "the worker logs the violation with the task's context")
}

func TestConnSendsBeforeErrorLimitsAreKnown(t *testing.T) {
	cc := &recordingConn{}
	errorLimits := &atomic.Pointer[Limits]{}
	service := workflowservice.NewWorkflowServiceClient(NewConn(cc, Limits{BlobWarn: 1_000_000}, errorLimits, nil))
	req := &workflowservice.RespondActivityTaskCompletedRequest{Result: payloads(10_000)}

	_, err := service.RespondActivityTaskCompleted(t.Context(), req)
	require.NoError(t, err)

	errorLimits.Store(&Limits{BlobError: 1000})
	_, err = service.RespondActivityTaskCompleted(t.Context(), req)
	require.Error(t, err)
	require.Equal(t, 1, cc.invoked)
}

func TestValidateErrorsChecksOnlyErrorLimits(t *testing.T) {
	limits := Limits{BlobWarn: 10, BlobError: 1000}

	err := ValidateErrors(&workflowservice.RespondActivityTaskCompletedRequest{Result: payloads(2000)}, limits)
	require.True(t, IsError(err))

	require.NoError(t, ValidateErrors(&workflowservice.RespondActivityTaskCompletedRequest{Result: payloads(500)}, limits))
}

func TestLogTags(t *testing.T) {
	require.Equal(t,
		[]any{"PayloadSize", int64(100), "PayloadSizeLimit", int64(50), "PayloadPath", "result"},
		LogTags(NewError(&Violation{Path: "result", Class: LimitClassBlob, Size: 100, Limit: 50})))
	require.Equal(t,
		[]any{"MemoSize", int64(100), "MemoSizeLimit", int64(50), "PayloadPath", "memo"},
		LogTags(NewError(&Violation{Path: "memo", Class: LimitClassMemo, Size: 100, Limit: 50})))
	require.Nil(t, LogTags(errors.New("other")))
}

func TestFailOversizedQueryResults(t *testing.T) {
	const message = "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit."

	t.Run("RespondQueryTaskCompletedRequest over the limit fails", func(t *testing.T) {
		msg := &workflowservice.RespondQueryTaskCompletedRequest{QueryResult: payloads(2000)}
		FailOversizedQueryResults(msg, 500)
		require.Nil(t, msg.GetQueryResult())
		require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, msg.GetCompletedType())
		require.Equal(t, message, msg.GetErrorMessage())
	})

	t.Run("RespondWorkflowTaskCompletedRequest fails only the oversized results", func(t *testing.T) {
		msg := &workflowservice.RespondWorkflowTaskCompletedRequest{QueryResults: map[string]*querypb.WorkflowQueryResult{
			"big":   {Answer: payloads(2000)},
			"small": {Answer: payloads(100)},
		}}
		FailOversizedQueryResults(msg, 500)
		require.Nil(t, msg.QueryResults["big"].GetAnswer())
		require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, msg.QueryResults["big"].GetResultType())
		require.Equal(t, message, msg.QueryResults["big"].GetErrorMessage())
		require.NotNil(t, msg.QueryResults["small"].GetAnswer())
		require.Equal(t, enumspb.QUERY_RESULT_TYPE_UNSPECIFIED, msg.QueryResults["small"].GetResultType())
	})

	t.Run("a failed query's failure details are not measured", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		cc := &recordingConn{}
		service := workflowservice.NewWorkflowServiceClient(NewConn(cc, Limits{BlobWarn: 100}, errorLimitsOf(500), logger))
		msg := &workflowservice.RespondQueryTaskCompletedRequest{
			CompletedType: enumspb.QUERY_RESULT_TYPE_FAILED,
			Failure: &failurepb.Failure{
				FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
					ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{Details: payloads(2000)},
				},
			},
		}

		FailOversizedQueryResults(msg, 500)
		_, err := service.RespondQueryTaskCompleted(t.Context(), msg)

		require.NoError(t, err)
		require.Equal(t, 1, cc.invoked)
		require.Empty(t, logger.Lines())
		require.NotNil(t, msg.GetFailure().GetApplicationFailureInfo().GetDetails())
	})

	t.Run("a zero limit leaves results alone", func(t *testing.T) {
		msg := &workflowservice.RespondQueryTaskCompletedRequest{QueryResult: payloads(2000)}
		FailOversizedQueryResults(msg, 0)
		require.NotNil(t, msg.GetQueryResult())
	})
}
