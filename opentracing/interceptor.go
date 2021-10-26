package opentracing

import (
	"context"
	"fmt"

	"github.com/opentracing/opentracing-go"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

const (
	defaultHeaderKey = "_tracer-data"
	workflowIDTagKey = "temporalWorkflowID"
	runIDTagKey      = "temporalRunID"
)

type spanContextKey struct{}

type rootInterceptor struct {
	interceptor.InterceptorBase
	tracer      opentracing.Tracer
	headerKey   string
	spanStarter func(t opentracing.Tracer, operationName string, opts ...opentracing.StartSpanOption) opentracing.Span
}

type InterceptorOption func(*rootInterceptor)

func WithSpanStarter(
	spanStarter func(t opentracing.Tracer, operationName string, opts ...opentracing.StartSpanOption) opentracing.Span,
) InterceptorOption {
	return func(r *rootInterceptor) { r.spanStarter = spanStarter }
}

func WithHeaderKey(key string) InterceptorOption {
	return func(r *rootInterceptor) { r.headerKey = key }
}

func NewInterceptor(tracer opentracing.Tracer, options ...InterceptorOption) interceptor.Interceptor {
	r := &rootInterceptor{tracer: tracer, headerKey: defaultHeaderKey}
	for _, option := range options {
		option(r)
	}
	if r.spanStarter == nil {
		r.spanStarter = func(
			tracer opentracing.Tracer,
			operationName string,
			opts ...opentracing.StartSpanOption,
		) opentracing.Span {
			return tracer.StartSpan(operationName, opts...)
		}
	}
	return r
}

func (r *rootInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	i := &clientOutboundInterceptor{root: r}
	i.Next = next
	return i
}

func (r *rootInterceptor) InterceptActivity(
	ctx context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &activityInboundInterceptor{root: r}
	i.Next = next
	return i
}

func (r *rootInterceptor) InterceptWorkflow(
	ctx workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &workflowInboundInterceptor{root: r}
	i.Next = next
	return i
}

type clientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase
	root *rootInterceptor
}

func (c *clientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := c.root.startSpanFromContext(ctx, &spanOptions{
		operation:     "StartWorkflow:" + in.WorkflowType,
		tags:          opentracing.Tags{workflowIDTagKey: in.Options.ID},
		writeToHeader: true,
	})
	if err != nil {
		return nil, err
	}
	defer span.Finish()

	return c.Next.ExecuteWorkflow(ctx, in)
}

func (c *clientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	// Start span and write to header
	span, ctx, err := c.root.startSpanFromContext(ctx, &spanOptions{
		operation:     "SignalWithStartWorkflow:" + in.WorkflowType,
		tags:          opentracing.Tags{workflowIDTagKey: in.Options.ID},
		writeToHeader: true,
	})
	if err != nil {
		return nil, err
	}
	defer span.Finish()

	return c.Next.SignalWithStartWorkflow(ctx, in)
}

type activityInboundInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
	root *rootInterceptor
}

func (a *activityInboundInterceptor) ExecuteActivity(
	ctx context.Context,
	in *interceptor.ExecuteActivityInput,
) (interface{}, error) {
	// Start span reading from header
	info := activity.GetInfo(ctx)
	span, ctx, err := a.root.startSpanFromContext(ctx, &spanOptions{
		operation: "RunActivity:" + info.ActivityType.Name,
		tags: opentracing.Tags{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		readFromHeader: true,
		parentRef:      opentracing.ChildOf,
	})
	if err != nil {
		return nil, err
	}
	defer span.Finish()

	return a.Next.ExecuteActivity(ctx, in)
}

type workflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
	root *rootInterceptor
}

func (w *workflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	i := &workflowOutboundInterceptor{root: w.root}
	i.Next = outbound
	return w.Next.Init(i)
}

func (w *workflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context,
	in *interceptor.ExecuteWorkflowInput,
) (interface{}, error) {
	// Start span reading from header
	info := workflow.GetInfo(ctx)
	span, ctx, err := w.root.startSpanFromWorkflowContext(ctx, &spanOptions{
		operation: "RunWorkflow:" + info.WorkflowType.Name,
		tags: opentracing.Tags{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		readFromHeader: true,
	})
	if err != nil {
		return nil, err
	}
	defer span.Finish()

	return w.Next.ExecuteWorkflow(ctx, in)
}

type workflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
	root *rootInterceptor
}

func (w *workflowOutboundInterceptor) ExecuteActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	// Start span writing to header
	span, ctx, err := w.startNonReplaySpan(ctx, "StartActivity:"+activityType, opentracing.ChildOf)
	if err != nil {
		return err
	}
	defer span.Finish()

	return w.Next.ExecuteActivity(ctx, activityType, args...)
}

func (w *workflowOutboundInterceptor) ExecuteLocalActivity(
	ctx workflow.Context,
	activityType string,
	args ...interface{},
) workflow.Future {
	// Start span writing to header
	span, ctx, err := w.startNonReplaySpan(ctx, "StartActivity:"+activityType, opentracing.ChildOf)
	if err != nil {
		return err
	}
	defer span.Finish()

	return w.Next.ExecuteLocalActivity(ctx, activityType, args...)
}

func (w *workflowOutboundInterceptor) ExecuteChildWorkflow(
	ctx workflow.Context,
	childWorkflowType string,
	args ...interface{},
) workflow.ChildWorkflowFuture {
	// Start span writing to header
	span, ctx, err := w.startNonReplaySpan(ctx, "StartChildWorkflow:"+childWorkflowType, opentracing.FollowsFrom)
	if err != nil {
		return err
	}
	defer span.Finish()

	return w.Next.ExecuteChildWorkflow(ctx, childWorkflowType, args...)
}

func (w *workflowOutboundInterceptor) NewContinueAsNewError(
	ctx workflow.Context,
	wfn interface{},
	args ...interface{},
) error {
	err := w.Next.NewContinueAsNewError(ctx, wfn, args...)
	if !workflow.IsReplaying(ctx) {
		if contErr, _ := err.(*workflow.ContinueAsNewError); contErr != nil {
			// Get the current span and write header
			if span, _ := ctx.Value(spanContextKey{}).(opentracing.Span); span != nil {
				if writeErr := w.root.writeSpanToHeader(span, interceptor.WorkflowHeader(ctx)); writeErr != nil {
					return fmt.Errorf("failed writing span when creating continue as new error: %w", writeErr)
				}
			}
		}
	}
	return err
}

var nopSpan = opentracing.NoopTracer{}.StartSpan("")

// Span always returned, even in replay. futErr is non-nil on error.
func (w *workflowOutboundInterceptor) startNonReplaySpan(
	ctx workflow.Context,
	operation string,
	parentRef func(opentracing.SpanContext) opentracing.SpanReference,
) (span opentracing.Span, newCtx workflow.Context, futErr workflow.ChildWorkflowFuture) {
	// Noop span if replaying
	if workflow.IsReplaying(ctx) {
		return nopSpan, ctx, nil
	}
	info := workflow.GetInfo(ctx)
	span, newCtx, err := w.root.startSpanFromWorkflowContext(ctx, &spanOptions{
		operation: operation,
		tags: opentracing.Tags{
			workflowIDTagKey: info.WorkflowExecution.ID,
			runIDTagKey:      info.WorkflowExecution.RunID,
		},
		writeToHeader: true,
		parentRef:     parentRef,
	})
	if err != nil {
		return nopSpan, ctx, newErrFut(ctx, err)
	}
	return span, newCtx, nil
}

type spanOptions struct {
	operation      string
	tags           opentracing.Tags
	readFromHeader bool
	writeToHeader  bool
	// If nil, defaults to opentracing.FollowsFrom
	parentRef func(opentracing.SpanContext) opentracing.SpanReference
}

func (r *rootInterceptor) startSpanFromContext(
	ctx context.Context,
	options *spanOptions,
) (opentracing.Span, context.Context, error) {
	span, err := r.startSpan(ctx, opentracing.SpanFromContext(ctx), interceptor.Header(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, context.WithValue(ctx, spanContextKey{}, span), nil
}

func (r *rootInterceptor) startSpanFromWorkflowContext(
	ctx workflow.Context,
	options *spanOptions,
) (opentracing.Span, workflow.Context, error) {
	span, err := r.startSpan(ctx, nil, interceptor.WorkflowHeader(ctx), options)
	if err != nil {
		return nil, nil, err
	}
	return span, workflow.WithValue(ctx, spanContextKey{}, span), nil
}

// Note, this does not put the span on the context
func (r *rootInterceptor) startSpan(
	ctx interface{ Value(interface{}) interface{} },
	// If nil, will be retrieved from context value
	parentSpan opentracing.Span,
	header map[string]*commonpb.Payload,
	options *spanOptions,
) (opentracing.Span, error) {
	var spanOpts []opentracing.StartSpanOption

	if options.parentRef == nil {
		options.parentRef = opentracing.FollowsFrom
	}

	// Use parent from header if allowed
	if options.readFromHeader {
		if span, err := r.readSpanFromHeader(header); err != nil {
			return nil, err
		} else if span != nil {
			spanOpts = append(spanOpts, options.parentRef(span))
		}
	}

	// If context parent not already there, try to get from value
	if parentSpan == nil {
		parentSpan, _ = ctx.Value(spanContextKey{}).(opentracing.Span)
	}
	if parentSpan != nil {
		spanOpts = append(spanOpts, options.parentRef(parentSpan.Context()))
	}

	// Start the span
	if len(options.tags) > 0 {
		spanOpts = append(spanOpts, options.tags)
	}
	span := r.spanStarter(r.tracer, options.operation, spanOpts...)

	// Put span in header if wanted
	if options.writeToHeader && header != nil {
		if err := r.writeSpanToHeader(span, header); err != nil {
			return nil, err
		}
	}
	return span, nil
}

func (r *rootInterceptor) readSpanFromHeader(
	header map[string]*commonpb.Payload,
) (opentracing.SpanContext, error) {
	// Get from map
	payload := header[r.headerKey]
	if payload == nil {
		return nil, nil
	}
	// Convert from the payload
	var data opentracing.TextMapCarrier
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &data); err != nil {
		return nil, err
	}
	// Extract
	return r.tracer.Extract(opentracing.TextMap, data)
}

func (r *rootInterceptor) writeSpanToHeader(span opentracing.Span, header map[string]*commonpb.Payload) error {
	// Serialize span to map
	data := opentracing.TextMapCarrier{}
	if err := r.tracer.Inject(span.Context(), opentracing.TextMap, data); err != nil {
		return err
	}
	// Convert to payload
	payload, err := converter.GetDefaultDataConverter().ToPayload(data)
	if err != nil {
		return err
	}
	// Put on header
	header[r.headerKey] = payload
	return nil
}

func newErrFut(ctx workflow.Context, err error) workflow.ChildWorkflowFuture {
	fut, set := workflow.NewFuture(ctx)
	set.SetError(err)
	return errFut{fut}
}

type errFut struct{ workflow.Future }

func (e errFut) GetChildWorkflowExecution() workflow.Future { return e }

func (e errFut) SignalChildWorkflow(ctx workflow.Context, signalName string, data interface{}) workflow.Future {
	return e
}
