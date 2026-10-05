package test_test

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const bindingTenantKey = "converter-binding-tenant"

type tenantValue string

// tenantConverter models selection of a tenant's key using context. The metadata
// is only a binding check, not encryption or authentication. SDK protocol values
// use the default converter; application values require the selected key.
type tenantConverter struct {
	converter.DataConverter
	tenant string
}

func (dc tenantConverter) WithContext(ctx context.Context) converter.DataConverter {
	dc.tenant, _ = ctx.Value(contextKey(bindingTenantKey)).(string)
	return dc
}

func (dc tenantConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	dc.tenant, _ = ctx.Value(contextKey(bindingTenantKey)).(string)
	return dc
}

func (dc tenantConverter) ToPayload(value any) (*commonpb.Payload, error) {
	_, applicationValue := value.(tenantValue)
	if applicationValue && dc.tenant == "" {
		return nil, fmt.Errorf("encode application value: tenant key is not bound")
	}
	payload, err := dc.DataConverter.ToPayload(value)
	if err != nil {
		return nil, err
	}
	if applicationValue {
		payload.Metadata["tenant-key"] = []byte(dc.tenant + "/key-v1")
	}
	return payload, nil
}

func (dc tenantConverter) FromPayload(payload *commonpb.Payload, value any) error {
	_, applicationValue := value.(*tenantValue)
	key := string(payload.GetMetadata()["tenant-key"])
	if applicationValue || key != "" {
		if dc.tenant == "" || key != dc.tenant+"/key-v1" {
			return fmt.Errorf("decode application value: tenant key %q does not match bound tenant %q", key, dc.tenant)
		}
	}
	return dc.DataConverter.FromPayload(payload, value)
}

func (dc tenantConverter) ToPayloads(values ...any) (*commonpb.Payloads, error) {
	payloads := &commonpb.Payloads{}
	for _, value := range values {
		payload, err := dc.ToPayload(value)
		if err != nil {
			return nil, err
		}
		payloads.Payloads = append(payloads.Payloads, payload)
	}
	return payloads, nil
}

func (dc tenantConverter) FromPayloads(payloads *commonpb.Payloads, values ...any) error {
	for i, value := range values {
		if i >= len(payloads.GetPayloads()) {
			break
		}
		if err := dc.FromPayload(payloads.Payloads[i], value); err != nil {
			return err
		}
	}
	return nil
}

func tenantBindingWorkflow(ctx workflow.Context) (tenantValue, error) {
	var mutable tenantValue
	for _, next := range []tenantValue{"first", "second", "second"} {
		err := workflow.MutableSideEffect(ctx, "tenant-value", func(workflow.Context) any {
			return next
		}, func(a, b any) bool {
			return a.(tenantValue) == b.(tenantValue)
		}).Get(&mutable)
		if err != nil {
			return "", err
		}
		if mutable != next {
			return "", fmt.Errorf("mutable side effect: got %q, want %q", mutable, next)
		}
	}
	if err := workflow.SetQueryHandler(ctx, "query", func(input tenantValue) (tenantValue, error) {
		return mutable + ":" + input, nil
	}); err != nil {
		return "", err
	}
	if err := workflow.SetUpdateHandler(ctx, "update", func(ctx workflow.Context, input tenantValue, wait bool) (tenantValue, error) {
		if wait {
			workflow.GetSignalChannel(ctx, "release").Receive(ctx, nil)
		}
		return mutable + ":" + input, nil
	}); err != nil {
		return "", err
	}
	workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return "", err
	}
	return mutable, nil
}

func (ts *IntegrationTestSuite) TestConverterContext_ServerBacked() {
	dc := tenantConverter{DataConverter: converter.GetDefaultDataConverter()}
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = dc
		options.ContextPropagators = []workflow.ContextPropagator{NewKeysPropagator([]string{bindingTenantKey})}
	})
	ts.NoError(err)
	defer c.Close()
	taskQueue := "tenant-binding-" + uuid.NewString()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(tenantBindingWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	baseCtx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		ts.Run(tenant, func() {
			parentAssertions := ts.Assertions
			ts.Assertions = require.New(ts.T())
			defer func() { ts.Assertions = parentAssertions }()
			ctx := context.WithValue(baseCtx, contextKey(bindingTenantKey), tenant)
			run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				ID: "tenant-binding-" + uuid.NewString(), TaskQueue: taskQueue,
			}, tenantBindingWorkflow)
			ts.NoError(err)
			query, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "query", tenantValue("query"))
			ts.NoError(err)
			var got tenantValue
			ts.NoError(query.Get(&got))
			ts.Equal(tenantValue("second:query"), got)
			wrongCtx := context.WithValue(baseCtx, contextKey(bindingTenantKey), "wrong-tenant")
			wrongQuery, err := c.QueryWorkflow(wrongCtx, run.GetID(), run.GetRunID(), "query", tenantValue("query"))
			ts.Error(err)
			ts.Nil(wrongQuery)

			immediate, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
				WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "immediate",
				UpdateName: "update", Args: []any{tenantValue("immediate"), false},
				WaitForStage: client.WorkflowUpdateStageCompleted,
			})
			ts.NoError(err)
			// A completed handle retains the UpdateWorkflow call's converter.
			ts.NoError(immediate.Get(ctx, &got))
			ts.Equal(tenantValue("second:immediate"), got)

			accepted, err := c.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
				WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "polled",
				UpdateName: "update", Args: []any{tenantValue("polled"), true},
				WaitForStage: client.WorkflowUpdateStageAccepted,
			})
			ts.NoError(err)
			ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "release", nil))
			ts.NoError(accepted.Get(ctx, &got))
			ts.Equal(tenantValue("second:polled"), got)
			retrieved := c.GetWorkflowUpdateHandle(client.GetWorkflowUpdateHandleOptions{
				WorkflowID: run.GetID(), RunID: run.GetRunID(), UpdateID: "polled",
			})
			ts.NoError(retrieved.Get(ctx, &got))
			ts.Equal(tenantValue("second:polled"), got)

			ts.NoError(c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "finish", nil))
			// The context passed to Get, not GetWorkflow, selects the decoding key.
			result := c.GetWorkflow(baseCtx, run.GetID(), run.GetRunID())
			ts.Error(result.Get(wrongCtx, &got))
			ts.NoError(result.Get(ctx, &got))
			ts.Equal(tenantValue("second"), got)
		})
	}

	ctx := context.WithValue(baseCtx, contextKey(bindingTenantKey), "terminated-tenant")
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "tenant-binding-" + uuid.NewString(), TaskQueue: taskQueue,
	}, tenantBindingWorkflow)
	ts.NoError(err)
	ts.NoError(c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "test termination", tenantValue("termination details")))
	history := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_CLOSE_EVENT)
	ts.True(history.HasNext())
	event, err := history.Next()
	ts.NoError(err)
	details := event.GetWorkflowExecutionTerminatedEventAttributes().GetDetails()
	ts.Len(details.GetPayloads(), 1)
	var got tenantValue
	ts.Error(dc.FromPayloads(details, &got))
	ts.NoError(dc.WithContext(ctx).FromPayloads(details, &got))
	ts.Equal(tenantValue("termination details"), got)
}
