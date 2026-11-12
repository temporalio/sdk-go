package internal

import (
	"time"

	common "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/protobuf/types/known/durationpb"
)

// WorkflowNamespace returns the namespace of the current workflow.
func WorkflowNamespace(ctx Context) string {
	return GetWorkflowInfo(ctx).Namespace
}

type SignalWithStartWorkflowModelRequest interface {
	GetNamespace() string
	GetID() string
}

func SignalWithStartWorkflowSerializationContext(request SignalWithStartWorkflowModelRequest) converter.WorkflowSerializationContext {
	return converter.WorkflowSerializationContext{
		Namespace:  request.GetNamespace(),
		WorkflowID: request.GetID(),
	}
}

// --- Duration (google.protobuf.Duration) ---

func DurationToProto(_ converter.DataConverter, d *time.Duration) (*durationpb.Duration, error) {
	if d == nil {
		return nil, nil
	}
	return durationpb.New(*d), nil
}

func DurationFromProto(_ converter.DataConverter, d *durationpb.Duration) (*time.Duration, error) {
	if d == nil {
		return nil, nil
	}
	value := d.AsDuration()
	return &value, nil
}

// --- TaskQueue (temporal.api.taskqueue.v1.TaskQueue) ---

func TaskQueueToProto(_ converter.DataConverter, name *string) (*taskqueuepb.TaskQueue, error) {
	if name == nil {
		return nil, nil
	}
	return &taskqueuepb.TaskQueue{Name: *name, Kind: enums.TASK_QUEUE_KIND_NORMAL}, nil
}

func TaskQueueFromProto(_ converter.DataConverter, tq *taskqueuepb.TaskQueue) (*string, error) {
	if tq == nil {
		return nil, nil
	}
	value := tq.GetName()
	return &value, nil
}

// --- RetryPolicy (temporal.api.common.v1.RetryPolicy) ---

func RetryPolicyToProto(_ converter.DataConverter, p *RetryPolicy) (*common.RetryPolicy, error) {
	return ConvertToPBRetryPolicy(p), nil
}

func RetryPolicyFromProto(_ converter.DataConverter, p *common.RetryPolicy) (*RetryPolicy, error) {
	return convertFromPBRetryPolicy(p), nil
}

// --- Priority (temporal.api.common.v1.Priority) ---

func PriorityToProto(_ converter.DataConverter, p *Priority) (*common.Priority, error) {
	if p == nil {
		return nil, nil
	}
	return ConvertToPBPriority(*p), nil
}

func PriorityFromProto(_ converter.DataConverter, p *common.Priority) (*Priority, error) {
	if p == nil {
		return nil, nil
	}
	value := convertFromPBPriority(p)
	return &value, nil
}

// --- WorkflowType (temporal.api.common.v1.WorkflowType) ---

func WorkflowTypeToProto(_ converter.DataConverter, name *string) (*common.WorkflowType, error) {
	if name == nil {
		return nil, nil
	}
	return &common.WorkflowType{Name: *name}, nil
}

func WorkflowTypeFromProto(_ converter.DataConverter, t *common.WorkflowType) (*string, error) {
	if t == nil {
		return nil, nil
	}
	value := t.GetName()
	return &value, nil
}

// --- Payload / Payloads (temporal.api.common.v1.Payload[s]) ---
func PayloadToProto(dc converter.DataConverter, value any) (*common.Payload, error) {
	return dc.ToPayload(value)
}

func PayloadFromProto(dc converter.DataConverter, payload *common.Payload) (any, error) {
	if payload == nil {
		return nil, nil
	}
	var value any
	if err := dc.FromPayload(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func PayloadsToProto(dc converter.DataConverter, values []any) (*common.Payloads, error) {
	return dc.ToPayloads(values...)
}

func PayloadsFromProto(dc converter.DataConverter, payloads *common.Payloads) ([]any, error) {
	if payloads == nil {
		return nil, nil
	}
	values := make([]any, 0, len(payloads.GetPayloads()))
	for _, payload := range payloads.GetPayloads() {
		value, err := PayloadFromProto(dc, payload)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// --- Failure (temporal.api.failure.v1.Failure) ---

func FailureToProto(dc converter.DataConverter, value error) (*failurepb.Failure, error) {
	failureConverter := NewDefaultFailureConverter(DefaultFailureConverterOptions{
		DataConverter: dc,
	})
	return failureConverter.ErrorToFailure(value), nil
}

func FailureFromProto(dc converter.DataConverter, failure *failurepb.Failure) (error, error) {
	if failure == nil {
		return nil, nil
	}
	failureConverter := NewDefaultFailureConverter(DefaultFailureConverterOptions{
		DataConverter: dc,
	})
	return failureConverter.FailureToError(failure), nil
}

// --- Memo (temporal.api.common.v1.Memo) ---

func MemoToProto(dc converter.DataConverter, memo map[string]any) (*common.Memo, error) {
	return GetWorkflowMemo(
		memo,
		dc,
		true,
	)
}

func MemoFromProto(dc converter.DataConverter, memo *common.Memo) (map[string]any, error) {
	if memo == nil {
		return nil, nil
	}
	result := make(map[string]any, len(memo.GetFields()))
	for key, payload := range memo.GetFields() {
		value, err := PayloadFromProto(dc, payload)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func HeaderToProto(_ converter.DataConverter, header map[string]*common.Payload) (*common.Header, error) {
	if header == nil {
		return nil, nil
	}
	return &common.Header{Fields: header}, nil
}

func HeaderFromProto(_ converter.DataConverter, header *common.Header) (map[string]*common.Payload, error) {
	if header == nil {
		return nil, nil
	}
	return header.GetFields(), nil
}

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---

func SearchAttributesToProto(_ converter.DataConverter, searchAttributes *SearchAttributes) (*common.SearchAttributes, error) {
	if searchAttributes == nil {
		return nil, nil
	}
	return SerializeSearchAttributes(nil, *searchAttributes)
}

func SearchAttributesFromProto(_ converter.DataConverter, searchAttributes *common.SearchAttributes) (*SearchAttributes, error) {
	if searchAttributes == nil {
		return nil, nil
	}

	value := convertToTypedSearchAttributes(ilog.NewDefaultLogger(), searchAttributes.GetIndexedFields())
	return &value, nil
}

// --- VersioningOverride (temporal.api.workflow.v1.VersioningOverride) ---

func systemNexusVersioningOverrideToProto(_ converter.DataConverter, versioningOverride *VersioningOverride) (*workflowpb.VersioningOverride, error) {
	if versioningOverride == nil {
		return nil, nil
	}
	return VersioningOverrideToProto(*versioningOverride), nil
}

func systemNexusVersioningOverrideFromProto(_ converter.DataConverter, versioningOverride *workflowpb.VersioningOverride) (*VersioningOverride, error) {
	if versioningOverride == nil {
		return nil, nil
	}

	value := versioningOverrideFromProto(versioningOverride)
	return &value, nil
}

// WorkflowFunctionName returns the registered name of a workflow function.
func WorkflowFunctionName(ctx Context, value any) string {
	name, err := GetWorkflowFunctionName(
		GetWorkflowEnvironment(ctx).GetRegistry(),
		value,
	)
	if err != nil {
		panic(err)
	}
	return name
}
