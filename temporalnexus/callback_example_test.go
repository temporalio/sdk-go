package temporalnexus_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// This example demonstrates an async Nexus operation backed by a standalone
// activity and standalone callback, with no handler workflow required.
//
// Flow:
//  1. CallerWorkflow invokes a Nexus operation via workflow.NexusClient.
//  2. The Nexus operation handler extracts the callback token from
//     StartOperationOptions and attaches it to the context.
//  3. The handler starts a standalone activity via client.ExecuteActivity().
//  4. A ClientOutboundInterceptor intercepts ExecuteActivity, reads the
//     callback info from context, and serializes it into the activity header.
//  5. The activity executes on a worker.
//  6. An ActivityInboundInterceptor reads the callback token from the activity
//     header after execution, then calls client.CompleteNexusOperation() to deliver
//     the result (or failure) back to the Nexus caller.

// --- Types ---

// GreetInput is the input for both the Nexus operation and the standalone activity.
type GreetInput struct {
	Name string
}

// GreetOutput is the output from the operation.
type GreetOutput struct {
	Greeting string
}

// GreetActivityFn is a simple activity that produces a greeting.
func GreetActivityFn(_ context.Context, input GreetInput) (GreetOutput, error) {
	return GreetOutput{Greeting: fmt.Sprintf("Hello, %s!", input.Name)}, nil
}

// --- Nexus Operation Reference ---

var greetOp = nexus.NewOperationReference[GreetInput, GreetOutput]("greet")

const (
	greetEndpoint  = "callback-demo-ep"
	greetService   = "greeting-service"
	greetTaskQueue = "callback-demo-tq"
)

// --- Caller Workflow ---

// CallerWorkflow calls the activity-backed Nexus operation and returns the result.
func CallerWorkflow(ctx workflow.Context, name string) (GreetOutput, error) {
	nexusClient := workflow.NewNexusClient(greetEndpoint, greetService)

	fut := nexusClient.ExecuteOperation(ctx, greetOp, GreetInput{Name: name}, workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 10 * time.Minute,
	})

	var exec workflow.NexusOperationExecution
	if err := fut.GetNexusOperationExecution().Get(ctx, &exec); err != nil {
		return GreetOutput{}, err
	}
	workflow.GetLogger(ctx).Info("nexus operation started", "token", exec.OperationToken)

	var result GreetOutput
	err := fut.Get(ctx, &result)
	return result, err
}

// --- Header key for propagating callback info ---

const callbackHeaderKey = "_nexus-callback-token"

// --- Context key for passing callback info from handler to client interceptor ---

type callbackCtxKey struct{}

// --- Nexus Operation Handler ---

// greetOperation implements nexus.Operation[GreetInput, GreetOutput] as an async
// operation backed by a standalone activity. The activity interceptor delivers
// the result via CompleteNexusOperation when the activity completes.
type greetOperation struct {
	nexus.UnimplementedOperation[GreetInput, GreetOutput]
}

func NewGreetOperation() nexus.Operation[GreetInput, GreetOutput] {
	return &greetOperation{}
}

func (o *greetOperation) Name() string { return "greet" }

func (o *greetOperation) Start(ctx context.Context, input GreetInput, opts nexus.StartOperationOptions) (nexus.HandlerStartOperationResult[GreetOutput], error) {
	c := temporalnexus.GetClient(ctx)
	opInfo := temporalnexus.GetOperationInfo(ctx)

	// Extract the callback token from the Nexus StartOperationOptions.
	if token := opts.CallbackHeader.Get("Temporal-Callback-Token"); token != "" {
		ctx = context.WithValue(ctx, callbackCtxKey{}, token)
	}

	// Start a standalone activity. The activity interceptor will deliver
	// the callback when it completes.
	temporalnexus.GetLogger(ctx).Info("starting activity for greet operation", "activity", "GreetActivityFn")
	handle, err := c.ExecuteActivity(ctx, client.StartActivityOptions{
		ID:                     opts.RequestID,
		TaskQueue:              opInfo.TaskQueue,
		ScheduleToCloseTimeout: 5 * time.Minute,
	}, GreetActivityFn, input)
	if err != nil {
		return nil, err
	}

	// Return async — the activity ID is the operation token.
	// The activity interceptor handles callback delivery.
	return &nexus.HandlerStartOperationResultAsync{OperationToken: handle.GetID()}, nil
}

// --- Client Interceptor: propagates callback token into activity headers ---

// NexusCallbackClientInterceptor intercepts ExecuteActivity to propagate
// the callback token from the context into the activity's headers.
type NexusCallbackClientInterceptor struct {
	interceptor.ClientInterceptorBase
}

type nexusCallbackClientOutbound struct {
	interceptor.ClientOutboundInterceptorBase
}

func (i *NexusCallbackClientInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	out := &nexusCallbackClientOutbound{}
	out.Next = next
	return out
}

func (o *nexusCallbackClientOutbound) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ClientExecuteActivityInput,
) (client.ActivityHandle, error) {
	// If callback token is in the context, serialize it into the header.
	if token, ok := ctx.Value(callbackCtxKey{}).(string); ok {
		payload, err := converter.GetDefaultDataConverter().ToPayload(token)
		if err == nil {
			header := interceptor.Header(ctx)
			if header != nil {
				header[callbackHeaderKey] = payload
			}
		}
	}
	return o.Next.ExecuteActivity(ctx, in)
}

// --- Activity Interceptor: reads headers and delivers callback ---

// NexusCallbackWorkerInterceptor intercepts activity execution. After the
// activity completes, if a callback token is present in the headers, it delivers
// the result to the Nexus caller via CompleteNexusOperation.
type NexusCallbackWorkerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

type nexusCallbackActivityInbound struct {
	interceptor.ActivityInboundInterceptorBase
	outbound interceptor.ActivityOutboundInterceptor
}

func (i *NexusCallbackWorkerInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	inbound := &nexusCallbackActivityInbound{}
	inbound.Next = next
	return inbound
}

func (a *nexusCallbackActivityInbound) Init(outbound interceptor.ActivityOutboundInterceptor) error {
	a.outbound = outbound
	return a.Next.Init(outbound)
}

func (a *nexusCallbackActivityInbound) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (any, error) {
	// Run the actual activity.
	result, err := a.Next.ExecuteActivity(ctx, in)

	// Check if callback token was propagated via headers.
	token := a.extractCallbackTokenFromHeaders(ctx)
	if token == "" {
		return result, err
	}

	// Get the client from the activity outbound interceptor.
	c := a.outbound.GetClient(ctx)

	// Determine the completion value: success result or error.
	var completion any
	if err != nil {
		completion = err
	} else {
		completion = result
	}

	// Deliver the result via CompleteNexusOperation.
	cbErr := c.CompleteNexusOperation(ctx, token, completion, client.CompleteNexusOperationOptions{})
	if cbErr != nil {
		activity.GetLogger(ctx).Warn("Failed to complete nexus operation", "error", cbErr)
		return nil, err
	}

	return result, err
}

// extractCallbackTokenFromHeaders reads the callback token from the activity's
// propagated headers, if present.
func (a *nexusCallbackActivityInbound) extractCallbackTokenFromHeaders(ctx context.Context) string {
	header := interceptor.Header(ctx)
	if header == nil {
		return ""
	}
	payload, ok := header[callbackHeaderKey]
	if !ok || payload == nil {
		return ""
	}
	var token string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &token); err != nil {
		return ""
	}
	return token
}

// --- Worker Setup ---

func TestExample_activityBackedNexusOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create the client with the client interceptor to propagate callback info.
	c, err := client.Dial(client.Options{
		Interceptors: []interceptor.ClientInterceptor{
			&NexusCallbackClientInterceptor{},
		},
	})
	if err != nil {
		t.Fatalf("Failed to dial client: %v", err)
	}

	// Create the Nexus endpoint so the caller workflow can reach the handler.
	_, err = c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: greetEndpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: "default",
						TaskQueue: greetTaskQueue,
					},
				},
			},
		},
	})
	if err != nil && !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("Failed to create Nexus endpoint: %v", err)
	}

	// Register the Nexus service with the activity-backed operation.
	service := nexus.NewService(greetService)
	_ = service.Register(NewGreetOperation())

	// Create the worker with the activity interceptor to deliver callbacks.
	w := worker.New(c, greetTaskQueue, worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{
			&NexusCallbackWorkerInterceptor{},
		},
	})
	w.RegisterNexusService(service)
	w.RegisterActivity(GreetActivityFn)
	w.RegisterWorkflow(CallerWorkflow)

	go w.Run(nil)

	// Start the caller workflow:
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "greeting-caller",
		TaskQueue:                greetTaskQueue,
		WorkflowExecutionTimeout: 5 * time.Second,
	}, CallerWorkflow, "World")
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}

	var result GreetOutput
	err = run.Get(ctx, &result)
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}
	fmt.Println(result.Greeting) // "Hello, World!"
}
