package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/converter"
)

type workflowClientReadInterceptor struct {
	ClientInterceptorBase
	ClientOutboundInterceptorBase
	getRun  func(context.Context, *ClientGetWorkflowRunInput) WorkflowRun
	result  func(context.Context, *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error)
	history func(context.Context, *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput
}

func (i *workflowClientReadInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	i.Next = next
	return i
}

func (i *workflowClientReadInterceptor) GetWorkflowRun(ctx context.Context, in *ClientGetWorkflowRunInput) WorkflowRun {
	if i.getRun != nil {
		return i.getRun(ctx, in)
	}
	return i.Next.GetWorkflowRun(ctx, in)
}

func (i *workflowClientReadInterceptor) GetWorkflowResult(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
	if i.result != nil {
		return i.result(ctx, in)
	}
	return i.Next.GetWorkflowResult(ctx, in)
}

func (i *workflowClientReadInterceptor) GetWorkflowHistory(ctx context.Context, in *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput {
	if i.history != nil {
		return i.history(ctx, in)
	}
	return i.Next.GetWorkflowHistory(ctx, in)
}

func newWorkflowClientReadTest(t *testing.T, interceptor *workflowClientReadInterceptor) (*WorkflowClient, *workflowservicemock.MockWorkflowServiceClient) {
	t.Helper()
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	client := NewServiceClient(service, nil, ClientOptions{
		Interceptors: []ClientInterceptor{&ClientInterceptorBase{}, interceptor, &ClientInterceptorBase{}},
	})
	t.Cleanup(client.Close)
	return client, service
}

func workflowClientReadCompleted(t *testing.T, value string) *historypb.HistoryEvent {
	t.Helper()
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(value)
	require.NoError(t, err)
	return &historypb.HistoryEvent{
		EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
			WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: payloads},
		},
	}
}

func TestWorkflowClientReadInterceptorGetWorkflowRun(t *testing.T) {
	for _, requestedRunID := range []string{"", "logical-run"} {
		t.Run(fmt.Sprintf("runID=%q", requestedRunID), func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			ctx := context.WithValue(t.Context(), contextKey("caller"), "caller")
			calls, requests := 0, 0
			interceptor.getRun = func(got context.Context, in *ClientGetWorkflowRunInput) WorkflowRun {
				calls++
				require.Equal(t, "caller", got.Value(contextKey("caller")))
				require.Equal(t, "logical-workflow", in.WorkflowID)
				require.Equal(t, requestedRunID, in.RunID)
				in.WorkflowID = "mapped-workflow"
				if in.RunID != "" {
					in.RunID = "resolved-run"
				}
				return interceptor.Next.GetWorkflowRun(context.WithValue(got, contextKey("hook"), "hook"), in)
			}
			if requestedRunID == "" {
				service.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(got context.Context, request *workflowservice.DescribeWorkflowExecutionRequest, _ ...grpc.CallOption) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
						requests++
						require.Equal(t, "hook", got.Value(contextKey("hook")))
						require.Equal(t, "mapped-workflow", request.Execution.WorkflowId)
						require.Empty(t, request.Execution.RunId)
						return &workflowservice.DescribeWorkflowExecutionResponse{
							WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
								Execution: &commonpb.WorkflowExecution{WorkflowId: "mapped-workflow", RunId: "resolved-run"},
							},
						}, nil
					})
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
					requests++
					require.Equal(t, "mapped-workflow", request.Execution.WorkflowId)
					require.Equal(t, "resolved-run", request.Execution.RunId)
					return &workflowservice.GetWorkflowExecutionHistoryResponse{
						History: &historypb.History{Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "result")}},
					}, nil
				})
			run := client.GetWorkflow(ctx, "logical-workflow", requestedRunID)
			require.Equal(t, 1, calls)
			require.Zero(t, requests, "obtaining a handle must remain lazy")
			require.Equal(t, "mapped-workflow", run.GetID())
			var result string
			require.NoError(t, run.Get(t.Context(), &result))
			require.Equal(t, "result", result)
			require.Equal(t, "resolved-run", run.GetRunID())
			require.Equal(t, 1, calls)
		})
	}
}

func TestWorkflowClientReadInterceptorGetWorkflowRunReplacement(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, _ := newWorkflowClientReadTest(t, interceptor)
	replacement := client.getWorkflow(t.Context(), "replacement-workflow", "replacement-run")
	interceptor.getRun = func(context.Context, *ClientGetWorkflowRunInput) WorkflowRun {
		return replacement
	}
	require.Same(t, replacement, client.GetWorkflow(t.Context(), workflowID, runID))
}

func TestWorkflowClientReadInterceptorResult(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, service := newWorkflowClientReadTest(t, interceptor)
	ctx := context.WithValue(t.Context(), contextKey("caller"), "caller")
	var result, decoded string
	getRunCalls := 0
	interceptor.getRun = func(ctx context.Context, in *ClientGetWorkflowRunInput) WorkflowRun {
		getRunCalls++
		return interceptor.Next.GetWorkflowRun(ctx, in)
	}
	calls := 0
	interceptor.result = func(got context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
		calls++
		require.Equal(t, "caller", got.Value(contextKey("caller")))
		require.Equal(t, workflowID, in.WorkflowID)
		require.Equal(t, runID, in.RunID)
		require.Equal(t, WorkflowRunGetOptions{}, in.Options)
		in.WorkflowID, in.RunID = "redirected-workflow", "redirected-run"
		out, err := interceptor.Next.GetWorkflowResult(context.WithValue(got, contextKey("hook"), "hook"), in)
		require.NoError(t, err)
		require.NoError(t, out.Error)
		require.Empty(t, result, "decoding into the caller's pointer must happen after the hook returns")
		require.NoError(t, out.Result.Get(&decoded))
		payloads, err := converter.GetDefaultDataConverter().ToPayloads(decoded + "-intercepted")
		require.NoError(t, err)
		out.Result = newEncodedValue(payloads, converter.GetDefaultDataConverter())
		return out, nil
	}
	interceptor.history = func(context.Context, *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput {
		t.Fatal("result polling must not invoke the public history hook")
		return nil
	}
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(got context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
			require.Equal(t, "hook", got.Value(contextKey("hook")))
			require.Equal(t, "redirected-workflow", request.Execution.WorkflowId)
			require.Equal(t, "redirected-run", request.Execution.RunId)
			require.True(t, request.WaitNewEvent)
			require.Equal(t, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT, request.HistoryEventFilterType)
			return &workflowservice.GetWorkflowExecutionHistoryResponse{
				History: &historypb.History{Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "result")}},
			}, nil
		})
	run := client.GetWorkflow(ctx, workflowID, runID)
	require.NoError(t, run.Get(ctx, &result))
	require.Equal(t, "result-intercepted", result)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, getRunCalls, "result redirection must not re-enter handle interception")
	require.Equal(t, workflowID, run.GetID())
	require.Equal(t, runID, run.GetRunID())
}

func TestWorkflowClientReadInterceptorResultNilPointer(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, service := newWorkflowClientReadTest(t, interceptor)
	calls := 0
	interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
		calls++
		out, err := interceptor.Next.GetWorkflowResult(ctx, in)
		require.NoError(t, err)
		require.NoError(t, out.Error)
		var result string
		require.NoError(t, out.Result.Get(&result))
		require.Equal(t, "result", result)
		return out, nil
	}
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
			History: &historypb.History{Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "result")}},
		}, nil)
	require.NoError(t, client.GetWorkflow(t.Context(), workflowID, runID).Get(t.Context(), nil))
	require.Equal(t, 1, calls)
}

func TestWorkflowClientReadInterceptorResultDecoding(t *testing.T) {
	var result string
	var wrongType int
	for _, test := range []struct {
		name        string
		destination any
		noResult    bool
		wantError   bool
	}{
		{name: "no payload", destination: "not a pointer", noResult: true},
		{name: "pointer", destination: &result},
		{name: "not a pointer", destination: "not a pointer", wantError: true},
		{name: "wrong type", destination: &wrongType, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				out, err := interceptor.Next.GetWorkflowResult(ctx, in)
				require.NoError(t, err, "destination validation and decoding happen after interception")
				require.NoError(t, out.Error)
				if test.noResult {
					require.Nil(t, out.Result)
				} else {
					require.NotNil(t, out.Result)
				}
				return out, nil
			}
			event := workflowClientReadCompleted(t, "result")
			if test.noResult {
				event.GetWorkflowExecutionCompletedEventAttributes().Result = nil
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
					History: &historypb.History{Events: []*historypb.HistoryEvent{event}},
				}, nil)
			err := client.GetWorkflow(t.Context(), workflowID, runID).Get(t.Context(), test.destination)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	require.Equal(t, "result", result)
}

func TestWorkflowClientReadInterceptorResultWorkflowError(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%v", replace), func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			expected := errors.New("replacement workflow error")
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				out, err := interceptor.Next.GetWorkflowResult(ctx, in)
				require.NoError(t, err, "workflow failures must not be retrieval errors")
				var workflowError *WorkflowExecutionError
				require.ErrorAs(t, out.Error, &workflowError)
				var applicationError *ApplicationError
				require.ErrorAs(t, out.Error, &applicationError)
				require.Equal(t, "failed", applicationError.Message())
				if replace {
					out.Error = expected
				}
				return out, nil
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
					History: &historypb.History{Events: []*historypb.HistoryEvent{{
						EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
						Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
							WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
								Failure: GetDefaultFailureConverter().ErrorToFailure(NewApplicationError("failed", "test", false, nil)),
							},
						},
					}}},
				}, nil)
			err := client.GetWorkflow(t.Context(), workflowID, runID).Get(t.Context(), nil)
			if replace {
				require.ErrorIs(t, err, expected)
			} else {
				var workflowError *WorkflowExecutionError
				require.ErrorAs(t, err, &workflowError)
			}
		})
	}
}

func TestWorkflowClientReadInterceptorResultEmptyRunID(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, service := newWorkflowClientReadTest(t, interceptor)
	intercepted := false
	interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
		intercepted = true
		require.Empty(t, in.RunID)
		return interceptor.Next.GetWorkflowResult(ctx, in)
	}
	service.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *workflowservice.DescribeWorkflowExecutionRequest, ...grpc.CallOption) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
			require.True(t, intercepted, "run-ID resolution must follow result interception")
			return &workflowservice.DescribeWorkflowExecutionResponse{
				WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
					Execution: &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
				},
			}, nil
		})
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
			require.Equal(t, runID, request.Execution.RunId)
			return &workflowservice.GetWorkflowExecutionHistoryResponse{
				History: &historypb.History{Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "result")}},
			}, nil
		})
	run := client.GetWorkflow(t.Context(), workflowID, "")
	var result string
	require.NoError(t, run.Get(t.Context(), &result))
	require.Equal(t, "result", result)
	require.Equal(t, runID, run.GetRunID())
}

func TestWorkflowClientReadInterceptorResultEmptyRunIDShortCircuit(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, _ := newWorkflowClientReadTest(t, interceptor)
	interceptor.result = func(context.Context, *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
		payloads, err := converter.GetDefaultDataConverter().ToPayloads("synthetic result")
		require.NoError(t, err)
		return &ClientGetWorkflowResultOutput{Result: newEncodedValue(payloads, converter.GetDefaultDataConverter())}, nil
	}
	var result string
	require.NoError(t, client.GetWorkflow(t.Context(), workflowID, "").Get(t.Context(), &result))
	require.Equal(t, "synthetic result", result)
}

func TestWorkflowClientReadInterceptorResultEmptyRunIDRedirect(t *testing.T) {
	for _, target := range []struct {
		workflowID string
		runID      string
	}{
		{workflowID: "mapped-workflow"},
		{workflowID: "mapped-workflow", runID: "mapped-run"},
		{workflowID: workflowID, runID: "mapped-run"},
	} {
		t.Run(fmt.Sprintf("%s/%s", target.workflowID, target.runID), func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				require.Equal(t, workflowID, in.WorkflowID)
				require.Empty(t, in.RunID)
				in.WorkflowID, in.RunID = target.workflowID, target.runID
				return interceptor.Next.GetWorkflowResult(ctx, in)
			}
			if target.runID == "" {
				service.EXPECT().DescribeWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, request *workflowservice.DescribeWorkflowExecutionRequest, _ ...grpc.CallOption) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
						require.Equal(t, target.workflowID, request.Execution.WorkflowId)
						require.Empty(t, request.Execution.RunId)
						return &workflowservice.DescribeWorkflowExecutionResponse{
							WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
								Execution: &commonpb.WorkflowExecution{WorkflowId: target.workflowID, RunId: "mapped-run"},
							},
						}, nil
					})
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
					require.Equal(t, target.workflowID, request.Execution.WorkflowId)
					require.Equal(t, "mapped-run", request.Execution.RunId)
					return &workflowservice.GetWorkflowExecutionHistoryResponse{
						History: &historypb.History{Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "result")}},
					}, nil
				})
			run := client.GetWorkflow(t.Context(), workflowID, "")
			var result string
			require.NoError(t, run.Get(t.Context(), &result))
			require.Equal(t, "result", result)
			require.Equal(t, workflowID, run.GetID())
		})
	}
}

func TestWorkflowClientReadInterceptorResultErrors(t *testing.T) {
	for _, shortCircuit := range []bool{false, true} {
		t.Run(fmt.Sprintf("shortCircuit=%v", shortCircuit), func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			expected := serviceerror.NewNotFound("workflow missing")
			calls := 0
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				calls++
				if shortCircuit {
					return nil, expected
				}
				out, err := interceptor.Next.GetWorkflowResult(ctx, in)
				require.Nil(t, out)
				require.ErrorIs(t, err, expected)
				return nil, fmt.Errorf("intercepted result: %w", err)
			}
			if !shortCircuit {
				service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, expected)
			}
			err := client.GetWorkflow(t.Context(), workflowID, runID).Get(t.Context(), nil)
			require.ErrorIs(t, err, expected)
			require.Equal(t, 1, calls)
		})
	}
}

func TestWorkflowClientReadInterceptorResultUnexpectedEvent(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, service := newWorkflowClientReadTest(t, interceptor)
	interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
		out, err := interceptor.Next.GetWorkflowResult(ctx, in)
		require.Nil(t, out)
		require.ErrorContains(t, err, "unexpected")
		return out, err
	}
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
			History: &historypb.History{Events: []*historypb.HistoryEvent{{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			}}},
		}, nil)
	require.ErrorContains(t, client.GetWorkflow(t.Context(), workflowID, runID).Get(t.Context(), nil), "unexpected")
}

func TestWorkflowClientReadInterceptorResultSerializationContext(t *testing.T) {
	for _, options := range []struct{ failed, runOnly bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		t.Run(fmt.Sprintf("failed=%v/runOnly=%v", options.failed, options.runOnly), func(t *testing.T) {
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
			interceptor := &workflowClientReadInterceptor{}
			client := NewServiceClient(service, nil, ClientOptions{
				DataConverter: dc,
				Interceptors:  []ClientInterceptor{interceptor},
			})
			t.Cleanup(client.Close)
			targetWorkflowID := "redirected-workflow"
			if options.runOnly {
				targetWorkflowID = workflowID
			}
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				in.WorkflowID, in.RunID = targetWorkflowID, "redirected-run"
				return interceptor.Next.GetWorkflowResult(ctx, in)
			}
			resultDC := converter.WithDataConverterSerializationContext(dc, converter.WorkflowSerializationContext{
				Namespace: client.namespace, WorkflowID: targetWorkflowID,
			})
			payloads, err := resultDC.ToPayloads("result")
			require.NoError(t, err)
			event := &historypb.HistoryEvent{
				EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
				Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
					WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{Result: payloads},
				},
			}
			if options.failed {
				fc := NewDefaultFailureConverter(DefaultFailureConverterOptions{DataConverter: resultDC})
				event = &historypb.HistoryEvent{
					EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
					Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
						WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
							Failure: fc.ErrorToFailure(NewApplicationError("failed", "test", false, nil, "result")),
						},
					},
				}
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetWorkflowExecutionHistoryResponse{
					History: &historypb.History{Events: []*historypb.HistoryEvent{event}},
				}, nil)
			service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.StartWorkflowExecutionResponse{RunId: runID}, nil)
			ctx := ContextWithNexusOperationContext(t.Context(), &NexusOperationContext{
				nexusSerializationContext: converter.NexusSerializationContext{
					Endpoint: "endpoint", Service: "service", Operation: "operation",
				},
			})
			run, err := client.ExecuteWorkflow(ctx, StartWorkflowOptions{ID: workflowID, TaskQueue: taskqueue}, workflowType)
			require.NoError(t, err)
			var result string
			err = run.Get(t.Context(), &result)
			if options.failed {
				require.ErrorContains(t, err, fmt.Sprintf(
					"workflow execution error (type: , workflowID: %s, runID: redirected-run)", targetWorkflowID))
				var applicationError *ApplicationError
				require.ErrorAs(t, err, &applicationError)
				require.NoError(t, applicationError.Details(&result))
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, "result", result)
		})
	}
}

func TestWorkflowClientReadInterceptorResultHandleSources(t *testing.T) {
	for _, source := range []string{"GetWorkflow", "ExecuteWorkflow", "SignalWithStartWorkflow", "UpdateWithStartWorkflow"} {
		t.Run(source, func(t *testing.T) {
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			ctx := t.Context()
			getRunCalls := 0
			interceptor.getRun = func(ctx context.Context, in *ClientGetWorkflowRunInput) WorkflowRun {
				getRunCalls++
				return interceptor.Next.GetWorkflowRun(ctx, in)
			}
			options := StartWorkflowOptions{
				ID: workflowID, TaskQueue: taskqueue,
				WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			}
			var run WorkflowRun
			var err error
			switch source {
			case "GetWorkflow":
				run = client.GetWorkflow(ctx, workflowID, runID)
			case "ExecuteWorkflow":
				service.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workflowservice.StartWorkflowExecutionResponse{RunId: runID}, nil)
				run, err = client.ExecuteWorkflow(ctx, options, workflowType)
			case "SignalWithStartWorkflow":
				service.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workflowservice.SignalWithStartWorkflowExecutionResponse{RunId: runID}, nil)
				run, err = client.SignalWithStartWorkflow(ctx, workflowID, "signal", nil, options, workflowType)
			case "UpdateWithStartWorkflow":
				service.EXPECT().ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&workflowservice.ExecuteMultiOperationResponse{
						Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
							{Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
								StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{RunId: runID},
							}},
							{Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{
								UpdateWorkflow: &workflowservice.UpdateWorkflowExecutionResponse{
									Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
								},
							}},
						},
					}, nil)
				start := client.NewWithStartWorkflowOperation(options, workflowType)
				_, err = client.UpdateWithStartWorkflow(ctx, UpdateWithStartWorkflowOptions{
					StartWorkflowOperation: start,
					UpdateOptions: UpdateWorkflowOptions{
						UpdateName: "update", WaitForStage: WorkflowUpdateStageCompleted,
					},
				})
				require.NoError(t, err)
				run, err = start.Get(ctx)
			}
			require.NoError(t, err)
			if source == "GetWorkflow" {
				require.Equal(t, 1, getRunCalls)
			} else {
				require.Zero(t, getRunCalls, "start interceptors already intercept creation of their handles")
			}
			calls := 0
			interceptor.result = func(_ context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				calls++
				require.Equal(t, workflowID, in.WorkflowID)
				require.Equal(t, runID, in.RunID)
				payloads, err := converter.GetDefaultDataConverter().ToPayloads("synthetic result")
				require.NoError(t, err)
				return &ClientGetWorkflowResultOutput{Result: newEncodedValue(payloads, converter.GetDefaultDataConverter())}, nil
			}
			var value string
			require.NoError(t, run.Get(ctx, &value))
			require.Equal(t, "synthetic result", value)
			require.NoError(t, run.GetWithOptions(ctx, &value, WorkflowRunGetOptions{}))
			require.Equal(t, 2, calls, "each public result call must invoke the hook")
		})
	}
}

func TestWorkflowClientReadInterceptorResultFollowingRuns(t *testing.T) {
	for _, options := range []struct{ disableFollowing, redirect bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		t.Run(fmt.Sprintf("disableFollowing=%v/redirect=%v", options.disableFollowing, options.redirect), func(t *testing.T) {
			disableFollowing := options.disableFollowing
			interceptor := &workflowClientReadInterceptor{}
			client, service := newWorkflowClientReadTest(t, interceptor)
			initialRunID := runID
			if options.redirect {
				initialRunID = "redirected-run"
			}
			calls := 0
			interceptor.result = func(ctx context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				calls++
				require.Equal(t, WorkflowRunGetOptions{DisableFollowingRuns: disableFollowing}, in.Options)
				require.Equal(t, runID, in.RunID)
				in.RunID = initialRunID
				out, err := interceptor.Next.GetWorkflowResult(ctx, in)
				require.NoError(t, err)
				if disableFollowing {
					var continued *ContinueAsNewError
					require.ErrorAs(t, out.Error, &continued)
				} else {
					require.NoError(t, out.Error)
					var value string
					require.NoError(t, out.Result.Get(&value))
					require.Equal(t, "finished", value)
				}
				return out, nil
			}
			interceptor.history = func(context.Context, *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput {
				t.Fatal("following runs must not invoke the history hook")
				return nil
			}
			service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
					require.Equal(t, initialRunID, request.Execution.RunId)
					return &workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{Events: []*historypb.HistoryEvent{{
						EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW,
						Attributes: &historypb.HistoryEvent_WorkflowExecutionContinuedAsNewEventAttributes{
							WorkflowExecutionContinuedAsNewEventAttributes: &historypb.WorkflowExecutionContinuedAsNewEventAttributes{NewExecutionRunId: "next-run"},
						},
					}}}}, nil
				})
			if !disableFollowing {
				service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
						require.Equal(t, "next-run", request.Execution.RunId)
						return &workflowservice.GetWorkflowExecutionHistoryResponse{History: &historypb.History{
							Events: []*historypb.HistoryEvent{workflowClientReadCompleted(t, "finished")},
						}}, nil
					})
			}
			var result string
			run := client.GetWorkflow(t.Context(), workflowID, runID)
			err := run.GetWithOptions(
				t.Context(), &result, WorkflowRunGetOptions{DisableFollowingRuns: disableFollowing})
			if disableFollowing {
				var continued *ContinueAsNewError
				require.ErrorAs(t, err, &continued)
			} else {
				require.NoError(t, err)
				require.Equal(t, "finished", result)
			}
			require.Equal(t, 1, calls)
			expectedRunID := runID
			if !disableFollowing && !options.redirect {
				expectedRunID = "next-run"
			}
			require.Equal(t, expectedRunID, run.GetRunID())
			interceptor.result = func(_ context.Context, in *ClientGetWorkflowResultInput) (*ClientGetWorkflowResultOutput, error) {
				require.Equal(t, expectedRunID, in.RunID)
				return &ClientGetWorkflowResultOutput{}, nil
			}
			require.NoError(t, run.Get(t.Context(), nil))
		})
	}
}

type workflowClientReadIterator struct {
	HistoryEventIterator
	next func() (*historypb.HistoryEvent, error)
}

func (i *workflowClientReadIterator) Next() (*historypb.HistoryEvent, error) {
	return i.next()
}

func TestWorkflowClientReadInterceptorHistory(t *testing.T) {
	interceptor := &workflowClientReadInterceptor{}
	client, service := newWorkflowClientReadTest(t, interceptor)
	ctx := context.WithValue(t.Context(), contextKey("caller"), "caller")
	calls, pages := 0, 0
	expectedErr := serviceerror.NewNotFound("history missing")
	interceptor.history = func(got context.Context, in *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput {
		calls++
		require.Equal(t, "caller", got.Value(contextKey("caller")))
		require.Equal(t, &ClientGetWorkflowHistoryInput{
			WorkflowID: workflowID, RunID: runID, IsLongPoll: false,
			FilterType: enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
		}, in)
		in.WorkflowID, in.RunID = "redirected-workflow", "redirected-run"
		in.IsLongPoll, in.FilterType = true, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT
		out := interceptor.Next.GetWorkflowHistory(context.WithValue(got, contextKey("hook"), "hook"), in)
		iter := out.Iterator
		out.Iterator = &workflowClientReadIterator{HistoryEventIterator: iter, next: func() (*historypb.HistoryEvent, error) {
			event, err := iter.Next()
			if err != nil {
				return nil, fmt.Errorf("intercepted history: %w", err)
			}
			event.EventId += 100
			return event, nil
		}}
		return out
	}
	service.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(got context.Context, request *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
			pages++
			require.Equal(t, "hook", got.Value(contextKey("hook")))
			require.Equal(t, "redirected-workflow", request.Execution.WorkflowId)
			require.Equal(t, "redirected-run", request.Execution.RunId)
			require.True(t, request.WaitNewEvent)
			require.Equal(t, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT, request.HistoryEventFilterType)
			if pages == 1 {
				require.Empty(t, request.NextPageToken)
				return &workflowservice.GetWorkflowExecutionHistoryResponse{
					History:       &historypb.History{Events: []*historypb.HistoryEvent{{EventId: 1}, {EventId: 2}}},
					NextPageToken: []byte("next"),
				}, nil
			}
			require.Equal(t, []byte("next"), request.NextPageToken)
			return nil, expectedErr
		}).Times(2)
	iter := client.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	require.Equal(t, 1, calls)
	require.Zero(t, pages, "history creation must remain lazy")
	for _, id := range []int64{101, 102} {
		require.True(t, iter.HasNext())
		require.True(t, iter.HasNext())
		event, err := iter.Next()
		require.NoError(t, err)
		require.Equal(t, id, event.EventId)
		require.Equal(t, 1, pages)
	}
	require.True(t, iter.HasNext())
	_, err := iter.Next()
	require.ErrorIs(t, err, expectedErr)
	require.Contains(t, err.Error(), "intercepted history")
	require.False(t, iter.HasNext())
	require.Equal(t, 2, pages)
	require.Equal(t, 1, calls)
}

func TestWorkflowClientReadInterceptorHistoryShortCircuit(t *testing.T) {
	expected := errors.New("synthetic history")
	interceptor := &workflowClientReadInterceptor{}
	client, _ := newWorkflowClientReadTest(t, interceptor)
	iter := &workflowClientReadIterator{next: func() (*historypb.HistoryEvent, error) { return nil, expected }}
	interceptor.history = func(context.Context, *ClientGetWorkflowHistoryInput) *ClientGetWorkflowHistoryOutput {
		return &ClientGetWorkflowHistoryOutput{Iterator: iter}
	}
	actual := client.GetWorkflowHistory(t.Context(), workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	require.Same(t, iter, actual)
	_, err := actual.Next()
	require.ErrorIs(t, err, expected)
}

type updateWithStartInterceptor struct {
	ClientInterceptorBase
	ClientOutboundInterceptorBase
	updateWithStart func(context.Context, *ClientUpdateWithStartWorkflowInput) (WorkflowUpdateHandle, error)
}

func (i *updateWithStartInterceptor) InterceptClient(next ClientOutboundInterceptor) ClientOutboundInterceptor {
	i.Next = next
	return i
}

func (i *updateWithStartInterceptor) UpdateWithStartWorkflow(ctx context.Context, in *ClientUpdateWithStartWorkflowInput) (WorkflowUpdateHandle, error) {
	return i.updateWithStart(ctx, in)
}

func TestUpdateWithStartInterceptorStartWorkflowInput(t *testing.T) {
	tests := []struct {
		name string
		// modify is applied by the interceptor; it returns the input to pass to Next.
		modify             func(in *ClientUpdateWithStartWorkflowInput) *ClientUpdateWithStartWorkflowInput
		expectedWorkflowID string
		expectedTaskQueue  string
	}{
		{
			name: "read only",
			modify: func(in *ClientUpdateWithStartWorkflowInput) *ClientUpdateWithStartWorkflowInput {
				return in
			},
			expectedWorkflowID: workflowID,
			expectedTaskQueue:  taskqueue,
		},
		{
			name: "modify in place",
			modify: func(in *ClientUpdateWithStartWorkflowInput) *ClientUpdateWithStartWorkflowInput {
				in.StartWorkflowInput.Options.TaskQueue = "intercepted-task-queue"
				return in
			},
			expectedWorkflowID: workflowID,
			expectedTaskQueue:  "intercepted-task-queue",
		},
		{
			name: "replace",
			modify: func(in *ClientUpdateWithStartWorkflowInput) *ClientUpdateWithStartWorkflowInput {
				options := *in.StartWorkflowInput.Options
				options.ID = "intercepted-workflow-id"
				in.StartWorkflowInput = &ClientExecuteWorkflowInput{
					Options:      &options,
					WorkflowType: in.StartWorkflowInput.WorkflowType,
					Args:         in.StartWorkflowInput.Args,
				}
				return in
			},
			expectedWorkflowID: "intercepted-workflow-id",
			expectedTaskQueue:  taskqueue,
		},
		{
			name: "nil falls back to operation input",
			modify: func(in *ClientUpdateWithStartWorkflowInput) *ClientUpdateWithStartWorkflowInput {
				// Simulates an interceptor written before StartWorkflowInput existed.
				return &ClientUpdateWithStartWorkflowInput{
					UpdateOptions:          in.UpdateOptions,
					StartWorkflowOperation: in.StartWorkflowOperation,
				}
			},
			expectedWorkflowID: workflowID,
			expectedTaskQueue:  taskqueue,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
				Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
			interceptor := &updateWithStartInterceptor{}
			interceptor.updateWithStart = func(ctx context.Context, in *ClientUpdateWithStartWorkflowInput) (WorkflowUpdateHandle, error) {
				require.NotNil(t, in.StartWorkflowInput)
				require.Equal(t, workflowID, in.StartWorkflowInput.Options.ID)
				require.Equal(t, taskqueue, in.StartWorkflowInput.Options.TaskQueue)
				require.Equal(t, workflowType, in.StartWorkflowInput.WorkflowType)
				require.Equal(t, []any{"arg"}, in.StartWorkflowInput.Args)
				return interceptor.Next.UpdateWithStartWorkflow(ctx, tc.modify(in))
			}
			client := NewServiceClient(service, nil, ClientOptions{Interceptors: []ClientInterceptor{interceptor}})
			t.Cleanup(client.Close)

			var startReq *workflowservice.StartWorkflowExecutionRequest
			service.EXPECT().ExecuteMultiOperation(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, req *workflowservice.ExecuteMultiOperationRequest, _ ...grpc.CallOption) (*workflowservice.ExecuteMultiOperationResponse, error) {
					startReq = req.Operations[0].GetStartWorkflow()
					return &workflowservice.ExecuteMultiOperationResponse{
						Responses: []*workflowservice.ExecuteMultiOperationResponse_Response{
							{Response: &workflowservice.ExecuteMultiOperationResponse_Response_StartWorkflow{
								StartWorkflow: &workflowservice.StartWorkflowExecutionResponse{RunId: runID},
							}},
							{Response: &workflowservice.ExecuteMultiOperationResponse_Response_UpdateWorkflow{
								UpdateWorkflow: &workflowservice.UpdateWorkflowExecutionResponse{
									Stage: enumspb.UPDATE_WORKFLOW_EXECUTION_LIFECYCLE_STAGE_COMPLETED,
								},
							}},
						},
					}, nil
				})

			start := client.NewWithStartWorkflowOperation(StartWorkflowOptions{
				ID: workflowID, TaskQueue: taskqueue,
				WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			}, workflowType, "arg")
			_, err := client.UpdateWithStartWorkflow(t.Context(), UpdateWithStartWorkflowOptions{
				StartWorkflowOperation: start,
				UpdateOptions: UpdateWorkflowOptions{
					UpdateName: "update", WaitForStage: WorkflowUpdateStageCompleted,
				},
			})
			require.NoError(t, err)

			require.Equal(t, tc.expectedWorkflowID, startReq.GetWorkflowId())
			require.Equal(t, tc.expectedTaskQueue, startReq.GetTaskQueue().GetName())
			run, err := start.Get(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.expectedWorkflowID, run.GetID())
		})
	}
}
