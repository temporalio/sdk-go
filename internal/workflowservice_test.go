package internal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

func TestSystemNexusContextPropagation(t *testing.T) {
	injectionErr := errors.New("context injection failed")
	for _, tt := range []struct {
		name         string
		noPropagator bool
		injectionErr error
		shortCircuit bool
	}{
		{name: "context propagation"},
		{name: "no propagators", noPropagator: true},
		{name: "propagator failure", injectionErr: injectionErr},
		{name: "interceptor short circuit", shortCircuit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
			propagator := &systemNexusHeaderTestPropagator{
				ContextPropagator: NewKeysPropagator([]string{"propagated"}),
				injectionErr:      tt.injectionErr,
			}
			if !tt.noPropagator {
				env.SetContextPropagators([]ContextPropagator{propagator})
			}
			terminal, ctx, err := newWorkflowContext(env.impl, nil)
			require.NoError(t, err)
			capture := &captureNexusSerializationEnv{WorkflowEnvironment: terminal.env}
			terminal.env = capture

			payload, err := converter.GetDefaultDataConverter().ToPayload("from interceptor")
			require.NoError(t, err)
			runID := "callee-run-id"
			started := true
			wantResponse := SystemNexus_SignalWithStartWorkflowResponse{RunID: &runID, Started: &started}
			outbound := &systemNexusHeaderTestOutbound{
				WorkflowOutboundInterceptorBase: WorkflowOutboundInterceptorBase{Next: terminal},
				payload:                         payload,
				shortCircuit:                    tt.shortCircuit,
				response:                        wantResponse,
			}
			parentHeader := map[string]*commonpb.Payload{"parent-header": payload}
			ctx = WithValue(ctx, headerKey{}, parentHeader)
			ctx = WithValue(ctx, workflowInterceptorContextKey, outbound)
			var operationErr error
			var response SystemNexus_SignalWithStartWorkflowResponse
			d, _ := newDispatcher(ctx, terminal, func(ctx Context) {
				future := SystemNexus_SignalWithStartWorkflow(
					ctx, SystemNexus_SignalWithStartWorkflowOptions{ID: "callee-id"},
					"signal", "signal argument", "callee", "workflow argument",
				)
				if len(capture.calls) != 0 {
					call := capture.calls[0]
					var responsePayload *commonpb.Payload
					responsePayload, operationErr = call.params.dataConverter.ToPayload(wantResponse)
					if operationErr != nil {
						return
					}
					call.started("", nil)
					call.completed(responsePayload, nil)
				}
				operationErr = future.Get(ctx, &response)
			}, func() bool { return false })
			d.interceptor = terminal
			defer d.Close()

			requireNoExecuteErr(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout))
			require.Equal(t, 1, outbound.calls)
			require.Equal(t, map[string]*commonpb.Payload{"parent-header": payload}, parentHeader)
			if tt.noPropagator || tt.shortCircuit {
				require.Zero(t, propagator.calls)
			} else {
				require.Equal(t, 1, propagator.calls)
			}
			if tt.injectionErr != nil {
				require.ErrorIs(t, operationErr, tt.injectionErr)
				require.Empty(t, capture.calls, "propagation failure must not schedule an operation")
				return
			}
			require.NoError(t, operationErr)
			require.Equal(t, wantResponse, response)
			if tt.shortCircuit {
				require.Empty(t, capture.calls, "interceptor short circuit must not schedule an operation")
				return
			}
			require.Len(t, capture.calls, 1)
			require.Empty(t, capture.calls[0].params.nexusHeader)
			var sent workflowservice.SignalWithStartWorkflowExecutionRequest
			require.NoError(t, proto.Unmarshal(capture.calls[0].params.input.GetData(), &sent))
			require.Equal(t, "intercepted-id", sent.GetWorkflowId())
			require.Equal(t, "intercepted-task-queue", sent.GetTaskQueue().GetName())
			require.Equal(t, enumspb.WORKFLOW_ID_REUSE_POLICY_UNSPECIFIED, sent.GetWorkflowIdReusePolicy(),
				"the terminal must not reapply defaults changed by an interceptor")
			require.Equal(t, "callee", sent.GetWorkflowType().GetName())
			wantHeaders := map[string]*commonpb.Payload{"interceptor-header": payload}
			if !tt.noPropagator {
				wantHeaders["propagated"] = payload
			}
			require.True(t, proto.Equal(&commonpb.Header{Fields: wantHeaders}, sent.GetHeader()),
				"got headers %v, want %v", sent.GetHeader(), wantHeaders)
		})
	}
}

func TestSystemNexusSignalWithStartReadOnly(t *testing.T) {
	env := new(WorkflowUnitTest).NewTestWorkflowEnvironment()
	terminal, ctx, err := newWorkflowContext(env.impl, nil)
	require.NoError(t, err)
	d, _ := newDispatcher(ctx, terminal, func(ctx Context) {
		SystemNexus_SignalWithStartWorkflow(
			ctx, SystemNexus_SignalWithStartWorkflowOptions{ID: "callee-id"},
			"signal", "signal argument", "callee",
		)
	}, func() bool { return false })
	d.interceptor = terminal
	d.setIsReadOnly(true)
	defer d.Close()

	require.ErrorContains(t, d.ExecuteUntilAllBlocked(defaultDeadlockDetectionTimeout), panicIllegalAccessCoroutineState)
}

type systemNexusHeaderTestPropagator struct {
	ContextPropagator
	injectionErr error
	calls        int
}

func (p *systemNexusHeaderTestPropagator) InjectFromWorkflow(ctx Context, writer HeaderWriter) error {
	p.calls++
	if p.injectionErr != nil {
		return p.injectionErr
	}
	return p.ContextPropagator.InjectFromWorkflow(ctx, writer)
}

type systemNexusHeaderTestOutbound struct {
	WorkflowOutboundInterceptorBase
	payload      *commonpb.Payload
	shortCircuit bool
	response     SystemNexus_SignalWithStartWorkflowResponse
	calls        int
}

func (i *systemNexusHeaderTestOutbound) SignalWithStartWorkflow(ctx Context, opts SystemNexus_SignalWithStartWorkflowOptions, signal string, signalArg any, workflowValue string, args ...any) Future {
	i.calls++
	headers := WorkflowHeader(ctx)
	if headers == nil || opts.TaskQueue == "" || opts.WorkflowIDReusePolicy == 0 {
		future, settable := NewFuture(ctx)
		settable.SetError(errors.New("headers and authored defaults must be initialized before interception"))
		return future
	}
	headers["interceptor-header"] = i.payload
	if i.shortCircuit {
		future, settable := NewFuture(ctx)
		settable.SetValue(i.response)
		return future
	}
	opts.ID = "intercepted-id"
	opts.TaskQueue = "intercepted-task-queue"
	opts.WorkflowIDReusePolicy = 0
	ctx = WithValue(ctx, contextKey("propagated"), "from interceptor")
	return i.Next.SignalWithStartWorkflow(ctx, opts, signal, signalArg, workflowValue, args...)
}
