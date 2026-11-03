package payloadlimits

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	querypb "go.temporal.io/api/query/v1"
	sdkpb "go.temporal.io/api/sdk/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/api/workflowservice/v1"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func payload(dataLen int) *commonpb.Payload {
	return &commonpb.Payload{Data: make([]byte, dataLen)}
}

func payloads(dataLen int) *commonpb.Payloads {
	return &commonpb.Payloads{Payloads: []*commonpb.Payload{payload(dataLen)}}
}

func memoWith(key string, dataLen int) *commonpb.Memo {
	return &commonpb.Memo{Fields: map[string]*commonpb.Payload{key: payload(dataLen)}}
}

// workerLimits warns at 10 bytes for both classes, with the given error thresholds.
func workerLimits(blobError, memoError int64) Limits {
	return Limits{BlobWarn: 10, BlobError: blobError, MemoWarn: 10, MemoError: memoError}
}

func wftWithCommand(cmd *commandpb.Command) *workflowservice.RespondWorkflowTaskCompletedRequest {
	return &workflowservice.RespondWorkflowTaskCompletedRequest{Commands: []*commandpb.Command{cmd}}
}

func requireViolation(t *testing.T, err error) *Violation {
	t.Helper()
	var v *Violation
	require.True(t, errors.As(err, &v), "expected a *Violation, got %v", err)
	return v
}

func TestValidateBlobFieldOverErrorLimit(t *testing.T) {
	req := &workflowservice.StartWorkflowExecutionRequest{Input: payloads(1000)}
	v := requireViolation(t, Validate(req, workerLimits(100, 100), nil))
	require.Equal(t, LimitClassBlob, v.Class)
	require.Equal(t, SeverityError, v.Severity)
	require.Equal(t, "input", v.Path)
	require.Greater(t, v.Size, int64(100))
	require.Equal(t, int64(100), v.Limit)
}

func TestValidateMemoFieldUsesMemoLimit(t *testing.T) {
	// Over the memo error limit but far under the blob one, so only the memo class can trip it.
	req := &workflowservice.StartWorkflowExecutionRequest{Memo: memoWith("k", 50)}
	v := requireViolation(t, Validate(req, Limits{BlobWarn: 10, BlobError: 1_000_000, MemoWarn: 10, MemoError: 20}, nil))
	require.Equal(t, LimitClassMemo, v.Class)
	require.Equal(t, "memo", v.Path)
}

func TestValidateWarnOnlyFieldNeverErrors(t *testing.T) {
	req := &workflowservice.RespondActivityTaskFailedRequest{
		Failure: &failurepb.Failure{Message: strings.Repeat("x", 10_000)},
	}
	require.NoError(t, Validate(req, workerLimits(100, 100), nil))
}

func TestValidateUnderLimit(t *testing.T) {
	req := &workflowservice.StartWorkflowExecutionRequest{Input: payloads(5)}
	require.NoError(t, Validate(req, workerLimits(100_000, 100_000), nil))
}

func TestValidateIgnoresRequestWithoutPayloadFields(t *testing.T) {
	require.NoError(t, Validate(&workflowservice.DescribeWorkflowExecutionRequest{}, workerLimits(1, 1), nil))
}

func TestValidateBlobClassedMemoIsMeasuredAsFieldsDataSum(t *testing.T) {
	// upserted_memo is blob-classed, so it's measured as key bytes + payload data bytes, not as the
	// serialized Memo.
	memo := &commonpb.Memo{Fields: map[string]*commonpb.Payload{"ab": payload(10), "cde": payload(20)}}
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_ModifyWorkflowPropertiesCommandAttributes{
			ModifyWorkflowPropertiesCommandAttributes: &commandpb.ModifyWorkflowPropertiesCommandAttributes{UpsertedMemo: memo},
		},
	})
	// (2 + 10) + (3 + 20) = 35
	v := requireViolation(t, Validate(req, workerLimits(30, 1_000_000), nil))
	require.Equal(t, LimitClassBlob, v.Class)
	require.Equal(t, "commands[0].modify_workflow_properties_command_attributes.upserted_memo", v.Path)
	require.Equal(t, int64(35), v.Size)
}

func TestValidateMarkerDetailsMapIsMeasuredAsPayloadsSum(t *testing.T) {
	details := map[string]*commonpb.Payloads{"marker": payloads(1000)}
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_RecordMarkerCommandAttributes{
			RecordMarkerCommandAttributes: &commandpb.RecordMarkerCommandAttributes{Details: details},
		},
	})
	v := requireViolation(t, Validate(req, workerLimits(100, 100), nil))
	require.Equal(t, "commands[0].record_marker_command_attributes.details", v.Path)
	require.Equal(t, int64(len("marker")+proto.Size(details["marker"])), v.Size)
}

func TestValidateSinglePayloadFieldIsMeasuredAsPayloadSize(t *testing.T) {
	input := payload(1000)
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_ScheduleNexusOperationCommandAttributes{
			ScheduleNexusOperationCommandAttributes: &commandpb.ScheduleNexusOperationCommandAttributes{Input: input},
		},
	})
	v := requireViolation(t, Validate(req, workerLimits(100, 100), nil))
	require.Equal(t, "commands[0].schedule_nexus_operation_command_attributes.input", v.Path)
	require.Equal(t, int64(proto.Size(input)), v.Size)
}

func TestValidateWholeFailureIsMeasuredAsMessageSize(t *testing.T) {
	failure := &failurepb.Failure{Message: strings.Repeat("x", 1000)}
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_FailWorkflowExecutionCommandAttributes{
			FailWorkflowExecutionCommandAttributes: &commandpb.FailWorkflowExecutionCommandAttributes{Failure: failure},
		},
	})
	v := requireViolation(t, Validate(req, workerLimits(100, 100), nil))
	require.Equal(t, "commands[0].fail_workflow_execution_command_attributes.failure", v.Path)
	require.Equal(t, int64(proto.Size(failure)), v.Size)
}

func TestViolationMessage(t *testing.T) {
	req := &workflowservice.StartWorkflowExecutionRequest{Input: payloads(1000)}
	err := Validate(req, workerLimits(100, 100), nil)
	require.EqualError(t, err, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
	require.Equal(t, "[TMPRL1103] Attempted to upload memo with size that exceeded the warning limit.",
		(&Violation{Class: LimitClassMemo, Severity: SeverityWarning}).Error())
}

func TestValidateLogsWarningsWhenNoErrors(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	req := &workflowservice.StartWorkflowExecutionRequest{Input: payloads(50), Memo: memoWith("k", 50)}
	require.NoError(t, Validate(req, workerLimits(0, 0), logger))
	lines := logger.Lines()
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "WARN  [TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
	require.Contains(t, lines[0], "PayloadSizeLimit 10 PayloadPath input")
	require.Contains(t, lines[1], "WARN  [TMPRL1103] Attempted to upload memo with size that exceeded the warning limit.")
	require.Contains(t, lines[1], "MemoSizeLimit 10 PayloadPath memo")
}

func TestValidateErrorsSuppressWarnings(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	// input exceeds its error limit; memo exceeds only its warning limit.
	req := &workflowservice.StartWorkflowExecutionRequest{Input: payloads(500), Memo: memoWith("k", 50)}
	v := requireViolation(t, Validate(req, workerLimits(100, 1_000_000), logger))
	require.Equal(t, "input", v.Path)
	lines := logger.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "ERROR [TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func TestCollectingSinkClassifiesErrorVsWarning(t *testing.T) {
	sink := &CollectingSink{Limits: workerLimits(100, 100)}
	sink.Check("over_error", LimitClassBlob, 200, true)
	sink.Check("over_warn", LimitClassBlob, 50, true)
	sink.Check("under_warn", LimitClassBlob, 5, true)
	require.Len(t, sink.Errors, 1)
	require.Equal(t, "over_error", sink.Errors[0].Path)
	require.Equal(t, int64(100), sink.Errors[0].Limit)
	require.Len(t, sink.Warnings, 1)
	require.Equal(t, "over_warn", sink.Warnings[0].Path)
	require.Equal(t, int64(10), sink.Warnings[0].Limit)
}

func TestCollectingSinkWarnOnlyFieldNeverErrors(t *testing.T) {
	sink := &CollectingSink{Limits: workerLimits(100, 100)}
	sink.Check("warn_only", LimitClassBlob, 5000, false)
	require.Empty(t, sink.Errors)
	require.Len(t, sink.Warnings, 1)
}

func TestCollectingSinkZeroErrorLimitOnlyWarns(t *testing.T) {
	sink := &CollectingSink{Limits: Limits{BlobWarn: 100}}
	sink.Check("big", LimitClassBlob, 101, true)
	require.Empty(t, sink.Errors)
	require.Len(t, sink.Warnings, 1)
}

func TestCollectingSinkZeroWarnDisablesWarnings(t *testing.T) {
	sink := &CollectingSink{}
	sink.Check("big", LimitClassBlob, 5000, true)
	require.Empty(t, sink.Errors)
	require.Empty(t, sink.Warnings)
}

func TestCollectingSinkRoutesMemoToMemoLimit(t *testing.T) {
	sink := &CollectingSink{Limits: workerLimits(1_000_000, 20)}
	sink.Check("blob_field", LimitClassBlob, 100, true)
	sink.Check("memo_field", LimitClassMemo, 100, true)
	require.Len(t, sink.Errors, 1)
	require.Equal(t, LimitClassMemo, sink.Errors[0].Class)
	require.Equal(t, "memo_field", sink.Errors[0].Path)
}

// recordingSink records the path of every checked field, regardless of size.
type recordingSink struct {
	path    Path
	visited []string
}

func (s *recordingSink) Check(fieldName string, _ LimitClass, _ int64, _ bool) {
	s.visited = append(s.visited, s.path.Leaf(fieldName))
}
func (s *recordingSink) Enter(name string)                 { s.path.Push(name) }
func (s *recordingSink) EnterIndex(name string, index int) { s.path.PushIndex(name, index) }
func (s *recordingSink) EnterKey(name string, key string)  { s.path.PushKey(name, key) }
func (s *recordingSink) Exit()                             { s.path.Pop() }

func visited(req proto.Message) []string {
	sink := &recordingSink{}
	dispatch(sink, req)
	return slices.Sorted(slices.Values(sink.visited))
}

func TestVisitsPayloadFieldsOfEachCommandWithPaths(t *testing.T) {
	req := &workflowservice.RespondWorkflowTaskCompletedRequest{Commands: []*commandpb.Command{
		{Attributes: &commandpb.Command_ScheduleActivityTaskCommandAttributes{
			ScheduleActivityTaskCommandAttributes: &commandpb.ScheduleActivityTaskCommandAttributes{Input: payloads(1)},
		}},
		{Attributes: &commandpb.Command_CompleteWorkflowExecutionCommandAttributes{
			CompleteWorkflowExecutionCommandAttributes: &commandpb.CompleteWorkflowExecutionCommandAttributes{Result: payloads(1)},
		}},
	}}
	require.Equal(t, []string{
		"commands[0].schedule_activity_task_command_attributes.input",
		"commands[1].complete_workflow_execution_command_attributes.result",
	}, visited(req))
}

func TestVisitsOnlyPresentFields(t *testing.T) {
	require.Equal(t, []string{"input"}, visited(&workflowservice.StartWorkflowExecutionRequest{Input: payloads(1)}))
}

func TestVisitsProtocolMessageBody(t *testing.T) {
	// Message.body is an Any, which is measured as a whole message like a Failure.
	req := &workflowservice.RespondWorkflowTaskCompletedRequest{
		Messages: []*protocolpb.Message{{Body: &anypb.Any{}}},
	}
	require.Equal(t, []string{"messages[0].body"}, visited(req))
}

// The not-validated tests assert the exact visited set rather than the absence of a violation, so
// that an accidental reclassification of a field the server doesn't check is caught.

func TestStartWorkflowVisitsOnlyValidatedFields(t *testing.T) {
	req := &workflowservice.StartWorkflowExecutionRequest{
		Input:                payloads(1),
		Memo:                 memoWith("k", 1),
		Header:               &commonpb.Header{Fields: map[string]*commonpb.Payload{"h": payload(1)}},
		SearchAttributes:     &commonpb.SearchAttributes{IndexedFields: map[string]*commonpb.Payload{"sa": payload(1)}},
		LastCompletionResult: payloads(1),
		ContinuedFailure:     &failurepb.Failure{Message: "boom"},
		UserMetadata:         &sdkpb.UserMetadata{Summary: payload(1), Details: payload(1)},
	}
	require.Equal(t, []string{"input", "memo"}, visited(req))
}

func TestContinueAsNewVisitsOnlyValidatedFields(t *testing.T) {
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_ContinueAsNewWorkflowExecutionCommandAttributes{
			ContinueAsNewWorkflowExecutionCommandAttributes: &commandpb.ContinueAsNewWorkflowExecutionCommandAttributes{
				Input:                payloads(1),
				Memo:                 memoWith("k", 1),
				Header:               &commonpb.Header{Fields: map[string]*commonpb.Payload{"h": payload(1)}},
				SearchAttributes:     &commonpb.SearchAttributes{IndexedFields: map[string]*commonpb.Payload{"sa": payload(1)}},
				Failure:              &failurepb.Failure{Message: "boom"},
				LastCompletionResult: payloads(1),
			},
		},
	})
	require.Equal(t, []string{
		"commands[0].continue_as_new_workflow_execution_command_attributes.input",
		"commands[0].continue_as_new_workflow_execution_command_attributes.memo",
	}, visited(req))
}

func TestRecordMarkerVisitsOnlyDetails(t *testing.T) {
	req := wftWithCommand(&commandpb.Command{
		Attributes: &commandpb.Command_RecordMarkerCommandAttributes{
			RecordMarkerCommandAttributes: &commandpb.RecordMarkerCommandAttributes{
				Details: map[string]*commonpb.Payloads{"marker": payloads(1)},
				Header:  &commonpb.Header{Fields: map[string]*commonpb.Payload{"h": payload(1)}},
				Failure: &failurepb.Failure{Message: "boom"},
			},
		},
	})
	require.Equal(t, []string{"commands[0].record_marker_command_attributes.details"}, visited(req))
}

func TestRequestsWithOnlyNotValidatedFieldsProduceNoChecks(t *testing.T) {
	require.Empty(t, visited(&workflowservice.TerminateWorkflowExecutionRequest{Details: payloads(5000)}))
	// Update args are enforced on delivery, through the protocol Message body on the worker's
	// completion.
	require.Empty(t, visited(&workflowservice.UpdateWorkflowExecutionRequest{
		Request: &updatepb.Request{Input: &updatepb.Input{
			Args:   payloads(5000),
			Header: &commonpb.Header{Fields: map[string]*commonpb.Payload{"h": payload(1)}},
		}},
	}))
}

func TestMapKeyedFieldsRenderTheKeyInThePath(t *testing.T) {
	req := &workflowservice.RespondWorkflowTaskCompletedRequest{
		QueryResults: map[string]*querypb.WorkflowQueryResult{"query-id": {Answer: payloads(1)}},
	}
	require.Equal(t, []string{"query_results[query-id].answer"}, visited(req))
}

func TestMapKeyedFieldsAreVisitedInKeyOrder(t *testing.T) {
	// Go map iteration is random; sorted keys keep the first reported violation deterministic.
	req := &workflowservice.RespondWorkflowTaskCompletedRequest{
		QueryResults: map[string]*querypb.WorkflowQueryResult{
			"c": {Answer: payloads(1)}, "a": {Answer: payloads(1)}, "b": {Answer: payloads(1)},
		},
	}
	for range 20 {
		sink := &recordingSink{}
		dispatch(sink, req)
		require.Equal(t, []string{
			"query_results[a].answer", "query_results[b].answer", "query_results[c].answer",
		}, sink.visited)
	}
}

// No validated field is repeated today, so only this test exercises messageSizeSum; the generator
// emits it as soon as one is.
func TestMessageSizeSum(t *testing.T) {
	a, b := payload(3), payload(40)
	require.Equal(t, int64(proto.Size(a)+proto.Size(b)), messageSizeSum([]*commonpb.Payload{a, b}))
	require.Equal(t, int64(0), messageSizeSum([]*failurepb.Failure(nil)))
}

func TestMapPayloadDataSumCountsUTF8KeyBytesAndRawData(t *testing.T) {
	// The server sums len(key) over Go strings, so this 2-rune key counts as 5 bytes.
	require.Equal(t, int64(15), mapPayloadDataSum(map[string]*commonpb.Payload{"é中": payload(10)}))
}
