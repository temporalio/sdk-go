package systemsupport

import (
	"fmt"
	"time"

	common "go.temporal.io/api/common/v1"
	enums "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	taskqueue "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/types/known/durationpb"
)

// WorkflowNamespace returns the namespace of the current workflow.
func WorkflowNamespace(ctx workflow.Context) string {
	return workflow.GetInfo(ctx).Namespace
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

func TaskQueueToProto(_ converter.DataConverter, name *string) (*taskqueue.TaskQueue, error) {
	if name == nil {
		return nil, nil
	}
	return &taskqueue.TaskQueue{Name: *name, Kind: enums.TASK_QUEUE_KIND_NORMAL}, nil
}

func TaskQueueFromProto(_ converter.DataConverter, tq *taskqueue.TaskQueue) (*string, error) {
	if tq == nil {
		return nil, nil
	}
	value := tq.GetName()
	return &value, nil
}

// --- RetryPolicy (temporal.api.common.v1.RetryPolicy) ---

func RetryPolicyToProto(_ converter.DataConverter, p *temporal.RetryPolicy) (*common.RetryPolicy, error) {
	return internal.ConvertToPBRetryPolicy(p), nil
}

func RetryPolicyFromProto(_ converter.DataConverter, p *common.RetryPolicy) (*temporal.RetryPolicy, error) {
	if p == nil {
		return nil, nil
	}
	policy := temporal.RetryPolicy{
		BackoffCoefficient:     p.GetBackoffCoefficient(),
		MaximumAttempts:        p.GetMaximumAttempts(),
		NonRetryableErrorTypes: p.GetNonRetryableErrorTypes(),
	}
	if interval := p.GetInitialInterval(); interval != nil {
		policy.InitialInterval = interval.AsDuration()
	}
	if interval := p.GetMaximumInterval(); interval != nil {
		policy.MaximumInterval = interval.AsDuration()
	}
	return &policy, nil
}

// --- Priority (temporal.api.common.v1.Priority) ---

func PriorityToProto(_ converter.DataConverter, p *temporal.Priority) (*common.Priority, error) {
	if p == nil {
		return nil, nil
	}
	return internal.ConvertToPBPriority(*p), nil
}

func PriorityFromProto(_ converter.DataConverter, p *common.Priority) (*temporal.Priority, error) {
	if p == nil {
		return nil, nil
	}
	return &temporal.Priority{
		PriorityKey:    int(p.GetPriorityKey()),
		FairnessKey:    p.GetFairnessKey(),
		FairnessWeight: p.GetFairnessWeight(),
	}, nil
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
	failureConverter := temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{
		DataConverter: dc,
	})
	return failureConverter.ErrorToFailure(value), nil
}

func FailureFromProto(dc converter.DataConverter, failure *failurepb.Failure) (error, error) {
	if failure == nil {
		return nil, nil
	}
	failureConverter := temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{
		DataConverter: dc,
	})
	return failureConverter.FailureToError(failure), nil
}

// --- Memo (temporal.api.common.v1.Memo) ---

func MemoToProto(dc converter.DataConverter, memo map[string]any) (*common.Memo, error) {
	return internal.GetWorkflowMemo(
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

func HeaderToProto(dc converter.DataConverter, header map[string]any) (*common.Header, error) {
	if header == nil {
		return nil, nil
	}
	fields := make(map[string]*common.Payload, len(header))
	for key, value := range header {
		payload, err := PayloadToProto(dc, value)
		if err != nil {
			return nil, fmt.Errorf("encode workflow header error: %v", err)
		}
		fields[key] = payload
	}
	return &common.Header{Fields: fields}, nil
}

func HeaderFromProto(dc converter.DataConverter, header *common.Header) (map[string]any, error) {
	if header == nil {
		return nil, nil
	}
	result := make(map[string]any, len(header.GetFields()))
	for key, payload := range header.GetFields() {
		value, err := PayloadFromProto(dc, payload)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---

func SearchAttributesToProto(_ converter.DataConverter, searchAttributes *temporal.SearchAttributes) (*common.SearchAttributes, error) {
	if searchAttributes == nil {
		return nil, nil
	}
	return internal.SerializeSearchAttributes(nil, *searchAttributes)
}

func SearchAttributesFromProto(_ converter.DataConverter, searchAttributes *common.SearchAttributes) (*temporal.SearchAttributes, error) {
	if searchAttributes == nil {
		return nil, nil
	}

	updates := make([]temporal.SearchAttributeUpdate, 0, len(searchAttributes.GetIndexedFields()))
	for name, payload := range searchAttributes.GetIndexedFields() {
		valueType, err := enums.IndexedValueTypeFromString(string(payload.GetMetadata()["type"]))
		if err != nil {
			return nil, fmt.Errorf("decode search attribute [%s] type error: %v", name, err)
		}
		switch valueType {
		case enums.INDEXED_VALUE_TYPE_BOOL:
			var value bool
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyBool(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_KEYWORD:
			var value string
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyKeyword(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_TEXT:
			var value string
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyString(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_INT:
			var value int64
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyInt64(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_DOUBLE:
			var value float64
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyFloat64(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_DATETIME:
			var value time.Time
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyTime(name).ValueSet(value))
		case enums.INDEXED_VALUE_TYPE_KEYWORD_LIST:
			var value []string
			err = converter.GetDefaultDataConverter().FromPayload(payload, &value)
			updates = append(updates, temporal.NewSearchAttributeKeyKeywordList(name).ValueSet(value))
		default:
			return nil, fmt.Errorf("decode search attribute [%s] error: unknown type %v", name, valueType)
		}
		if err != nil {
			return nil, fmt.Errorf("decode search attribute [%s] error: %v", name, err)
		}
	}
	value := temporal.NewSearchAttributes(updates...)
	return &value, nil
}

// --- VersioningOverride (temporal.api.workflow.v1.VersioningOverride) ---

func VersioningOverrideToProto(_ converter.DataConverter, versioningOverride *client.VersioningOverride) (*workflowpb.VersioningOverride, error) {
	if versioningOverride == nil {
		return nil, nil
	}
	return internal.VersioningOverrideToProto(*versioningOverride), nil
}

func VersioningOverrideFromProto(_ converter.DataConverter, versioningOverride *workflowpb.VersioningOverride) (*client.VersioningOverride, error) {
	if versioningOverride == nil {
		return nil, nil
	}

	var value client.VersioningOverride
	switch override := versioningOverride.GetOverride().(type) {
	case *workflowpb.VersioningOverride_AutoUpgrade:
		value = &client.AutoUpgradeVersioningOverride{}
	case *workflowpb.VersioningOverride_Pinned:
		version := override.Pinned.GetVersion()
		value = &client.PinnedVersioningOverride{Version: worker.WorkerDeploymentVersion{
			DeploymentName: version.GetDeploymentName(),
			BuildID:        version.GetBuildId(),
		}}
	}
	if value == nil {
		switch versioningOverride.GetBehavior() {
		case enums.VERSIONING_BEHAVIOR_AUTO_UPGRADE:
			value = &client.AutoUpgradeVersioningOverride{}
		case enums.VERSIONING_BEHAVIOR_PINNED:
			deploymentName := versioningOverride.GetDeployment().GetSeriesName()
			buildID := versioningOverride.GetDeployment().GetBuildId()
			value = &client.PinnedVersioningOverride{Version: worker.WorkerDeploymentVersion{
				DeploymentName: deploymentName,
				BuildID:        buildID,
			}}
		}
	}
	return &value, nil
}

// WorkflowFunctionName returns the registered name of a workflow function.
func WorkflowFunctionName(ctx workflow.Context, value any) string {
	name, err := internal.GetWorkflowFunctionName(
		internal.GetWorkflowEnvironment(ctx).GetRegistry(),
		value,
	)
	if err != nil {
		panic(err)
	}
	return name
}
