package test_test

import (
	"context"
	"fmt"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const (
	converterPropagationKey = "converter-propagated"
	rawPropagationKey       = "raw-propagated"
)

type converterContextPropagator struct {
	dataConverter    converter.DataConverter
	useDataConverter bool
}

var _ workflow.DataConverterContextPropagator = (*converterContextPropagator)(nil)

func (p *converterContextPropagator) WithDataConverter(dc converter.DataConverter) workflow.ContextPropagator {
	bound := *p
	bound.dataConverter = dc
	return &bound
}

func (p *converterContextPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	return p.inject(ctx.Value(contextKey(converterPropagationKey)), writer)
}

func (p *converterContextPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	return p.inject(ctx.Value(contextKey(converterPropagationKey)), writer)
}

func (p *converterContextPropagator) inject(value interface{}, writer workflow.HeaderWriter) error {
	if p.dataConverter == nil {
		return fmt.Errorf("context propagator did not receive a data converter before injection")
	}
	dc := converter.GetDefaultDataConverter()
	if p.useDataConverter {
		dc = p.dataConverter
	}
	payload, err := dc.ToPayload(value)
	if err != nil {
		return err
	}
	writer.Set(converterPropagationKey, payload)
	return nil
}

func (p *converterContextPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	value, err := p.extract(reader)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, contextKey(converterPropagationKey), value), nil
}

func (p *converterContextPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	value, err := p.extract(reader)
	if err != nil {
		return ctx, err
	}
	return workflow.WithValue(ctx, contextKey(converterPropagationKey), value), nil
}

func (p *converterContextPropagator) extract(reader workflow.HeaderReader) (string, error) {
	if p.dataConverter == nil {
		return "", fmt.Errorf("context propagator did not receive a data converter before extraction")
	}
	payload, ok := reader.Get(converterPropagationKey)
	if !ok {
		return "", fmt.Errorf("missing propagated header %q", converterPropagationKey)
	}
	var value string
	dc := converter.GetDefaultDataConverter()
	if p.useDataConverter {
		dc = p.dataConverter
	}
	err := dc.FromPayload(payload, &value)
	return value, err
}

func converterPropagationActivity(ctx context.Context) ([]string, error) {
	return []string{
		fmt.Sprint(ctx.Value(contextKey(converterPropagationKey))),
		fmt.Sprint(ctx.Value(contextKey(rawPropagationKey))),
	}, nil
}

func converterPropagationWorkflow(ctx workflow.Context, startChild bool) ([]string, error) {
	result := []string{
		fmt.Sprint(ctx.Value(contextKey(converterPropagationKey))),
		fmt.Sprint(ctx.Value(contextKey(rawPropagationKey))),
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var activityResult []string
	if err := workflow.ExecuteActivity(ctx, converterPropagationActivity).Get(ctx, &activityResult); err != nil {
		return nil, err
	}
	result = append(result, activityResult...)
	if startChild {
		ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID + "-child",
		})
		var childResult []string
		if err := workflow.ExecuteChildWorkflow(ctx, converterPropagationWorkflow, false).Get(ctx, &childResult); err != nil {
			return nil, err
		}
		result = append(result, childResult...)
	}
	return result, nil
}

func (ts *IntegrationTestSuite) TestContextPropagatorDataConverter() {
	ts.testContextPropagatorDataConverter(true)
}

func (ts *IntegrationTestSuite) TestContextPropagatorDataConverterDisabled() {
	ts.testContextPropagatorDataConverter(false)
}

func (ts *IntegrationTestSuite) testContextPropagatorDataConverter(useDataConverter bool) {
	defaultDC := converter.GetDefaultDataConverter()
	sharedPropagator := &converterContextPropagator{
		dataConverter: defaultDC, useDataConverter: useDataConverter,
	}
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &intTestSigningCodec{})
	propagators := []workflow.ContextPropagator{
		sharedPropagator,
		NewKeysPropagator([]string{rawPropagationKey}),
	}
	c, err := ts.newDefaultClient(func(options *client.Options) {
		options.DataConverter = dc
		options.ContextPropagators = propagators
	})
	ts.Require().NoError(err)
	defer c.Close()

	taskQueue := "context-propagator-converter-" + ts.T().Name()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(converterPropagationWorkflow)
	w.RegisterActivity(converterPropagationActivity)
	ts.Require().NoError(w.Start())
	defer w.Stop()

	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		DataConverter:      dc,
		ContextPropagators: propagators,
	})
	ts.Require().NoError(err)
	replayer.RegisterWorkflow(converterPropagationWorkflow)

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, contextKey(converterPropagationKey), "codec-value")
	ctx = context.WithValue(ctx, contextKey(rawPropagationKey), "raw-value")

	// Distinct workflow IDs require distinct converter bindings on the same propagator.
	runs := make([]client.WorkflowRun, 2)
	for i := range runs {
		runs[i], err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        fmt.Sprintf("%s-%d", taskQueue, i),
			TaskQueue: taskQueue,
		}, converterPropagationWorkflow, true)
		ts.Require().NoError(err)
	}
	for _, run := range runs {
		var result []string
		ts.Require().NoError(run.Get(ctx, &result))
		ts.Equal([]string{
			"codec-value", "raw-value",
			"codec-value", "raw-value",
			"codec-value", "raw-value",
			"codec-value", "raw-value",
		}, result)

		for _, workflowID := range []string{run.GetID(), run.GetID() + "-child"} {
			history := c.GetWorkflowHistory(ctx, workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			var events []*historypb.HistoryEvent
			var starts, activities, children int
			for history.HasNext() {
				event, err := history.Next()
				ts.Require().NoError(err)
				events = append(events, event)
				switch {
				case event.GetWorkflowExecutionStartedEventAttributes() != nil:
					starts++
					ts.assertConverterPropagationHeader(event.GetWorkflowExecutionStartedEventAttributes().GetHeader(), workflowID, useDataConverter)
				case event.GetActivityTaskScheduledEventAttributes() != nil:
					activities++
					attrs := event.GetActivityTaskScheduledEventAttributes()
					ts.assertConverterPropagationHeader(attrs.GetHeader(), workflowID+":"+attrs.GetActivityType().GetName(), useDataConverter)
				case event.GetStartChildWorkflowExecutionInitiatedEventAttributes() != nil:
					children++
					attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes()
					ts.assertConverterPropagationHeader(attrs.GetHeader(), attrs.GetWorkflowId(), useDataConverter)
				}
			}
			ts.Equal(1, starts)
			ts.Equal(1, activities)
			if workflowID == run.GetID() {
				ts.Equal(1, children)
			} else {
				ts.Zero(children)
			}
			ts.Require().NoError(replayer.ReplayWorkflowHistoryWithOptions(nil, &historypb.History{Events: events},
				worker.ReplayWorkflowHistoryOptions{OriginalExecution: workflow.Execution{ID: workflowID}}))
		}
	}
	ts.Same(defaultDC, sharedPropagator.dataConverter, "operation-bound converters must not mutate the shared propagator")
}

func (ts *IntegrationTestSuite) assertConverterPropagationHeader(header *commonpb.Header, signature string, useDataConverter bool) {
	ts.Require().NotNil(header)
	encoded := header.Fields[converterPropagationKey]
	ts.Require().NotNil(encoded)
	dc := converter.GetDefaultDataConverter()
	if useDataConverter {
		ts.Equal(signature, string(encoded.Metadata["ctx-signature"]))
		dc = converter.NewCodecDataConverter(dc, &intTestSigningCodec{signature: signature})
	} else {
		ts.NotContains(encoded.Metadata, "ctx-signature")
	}
	var value string
	ts.Require().NoError(dc.FromPayload(encoded, &value))
	ts.Equal("codec-value", value)

	raw := header.Fields[rawPropagationKey]
	ts.Require().NotNil(raw)
	expectedRaw, err := converter.GetDefaultDataConverter().ToPayload("raw-value")
	ts.Require().NoError(err)
	ts.Equal(expectedRaw, raw, "legacy propagator headers must remain untouched by the configured codec")
}
