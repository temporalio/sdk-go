package internal

import commonpb "go.temporal.io/api/common/v1"

type systemnexus_workflowHeader = map[string]*commonpb.Payload

func systemnexus_contextWithHeader(ctx Context, header *map[string]*commonpb.Payload) Context {
	if *header == nil {
		*header = make(map[string]*commonpb.Payload)
	}
	return WithValue(ctx, headerKey{}, *header)
}

func systemnexus_failedFuture(ctx Context, err error) NexusOperationFuture {
	mainFuture, mainSettable := newDecodeFuture(ctx, nil)
	executionFuture, executionSettable := NewFuture(ctx)
	mainSettable.Set(nil, err)
	executionSettable.Set(nil, err)
	return &nexusOperationFutureImpl{
		decodeFutureImpl: mainFuture.(*decodeFutureImpl),
		executionFuture:  executionFuture.(*futureImpl),
	}
}

func (wc *workflowEnvironmentInterceptor) systemnexus_executeOperation(
	ctx Context, input ExecuteNexusOperationInput,
) NexusOperationFuture {
	if err := ctx.Err(); err != nil {
		return systemnexus_failedFuture(ctx, err)
	}
	if WorkflowHeader(ctx) != nil {
		if _, err := workflowHeaderPropagated(ctx, getWorkflowEnvOptions(ctx).ContextPropagators); err != nil {
			return systemnexus_failedFuture(ctx, err)
		}
	}
	return wc.scheduleNexusOperation(ctx, input)
}
