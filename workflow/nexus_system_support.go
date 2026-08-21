package workflow

import (
	"time"

	common "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	taskqueue "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/types/known/durationpb"
)

// --- Duration (google.protobuf.Duration) ---

func durationToProto(_ Context, d *time.Duration) (*durationpb.Duration, error) {
	if d == nil {
		return nil, nil
	}
	return durationpb.New(*d), nil
}

// --- TaskQueue (temporal.api.taskqueue.v1.TaskQueue) ---

func taskQueueToProto(_ Context, name *string) (*taskqueue.TaskQueue, error) {
	if name == nil {
		return nil, nil
	}
	return &taskqueue.TaskQueue{Name: *name, Kind: enums.TASK_QUEUE_KIND_NORMAL}, nil
}

// --- RetryPolicy (temporal.api.common.v1.RetryPolicy) ---

func retryPolicyToProto(_ Context, p *temporal.RetryPolicy) (*common.RetryPolicy, error) {
	return internal.ConvertToPBRetryPolicy(p), nil
}

// --- Priority (temporal.api.common.v1.Priority) ---

func priorityToProto(_ Context, p *temporal.Priority) (*common.Priority, error) {
	if p == nil {
		return nil, nil
	}
	return internal.ConvertToPBPriority(*p), nil
}

// --- WorkflowType (temporal.api.common.v1.WorkflowType) ---

func workflowTypeToProto(_ Context, name *string) (*common.WorkflowType, error) {
	if name == nil {
		return nil, nil
	}
	return &common.WorkflowType{Name: *name}, nil
}

// --- Payload / Payloads (temporal.api.common.v1.Payload[s]) ---

func payloadToProto(ctx Context, value any) (*common.Payload, error) {
	return internal.GetDataConverterFromWorkflowContext(ctx).ToPayload(value)
}

func payloadFromProto(ctx Context, payload *common.Payload) (any, error) {
	if payload == nil {
		return nil, nil
	}
	var value any
	if err := internal.GetDataConverterFromWorkflowContext(ctx).FromPayload(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func payloadsToProto(ctx Context, values []any) (*common.Payloads, error) {
	return internal.GetDataConverterFromWorkflowContext(ctx).ToPayloads(values...)
}

// --- Memo (temporal.api.common.v1.Memo) ---

func memoToProto(ctx Context, memo map[string]any) (*common.Memo, error) {
	return internal.EncodeWorkflowMemo(ctx, memo)
}

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---

func searchAttributesToProto(_ Context, searchAttributes *temporal.SearchAttributes) (*common.SearchAttributes, error) {
	if searchAttributes == nil {
		return nil, nil
	}
	return internal.SerializeSearchAttributes(nil, *searchAttributes)
}

// --- VersioningOverride (temporal.api.workflow.v1.VersioningOverride) ---

func versioningOverrideToProto(_ Context, versioningOverride *client.VersioningOverride) (*workflowpb.VersioningOverride, error) {
	if versioningOverride == nil {
		return nil, nil
	}
	return internal.VersioningOverrideToProto(*versioningOverride), nil
}
