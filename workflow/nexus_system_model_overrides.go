// Hand-written proto converter functions for Temporal semantic types.
//
// The generated service file references these converters by name when a WIT
// type is replaced with a native Temporal Go SDK type (via `@nexus.type
// go=...`). Each function translates between the native value and the protobuf
// message that the Temporal SDK serializes onto the wire, keeping the Go
// bindings wire-compatible with the Python and TypeScript bindings.
//
// Converters are pure structural translations: a `nil` input always produces a
// `nil` output. They never invent zero values for absent data. The generated
// service file owns all presence/optionality logic: it passes pointers for
// required values and dereferences results with a zero fallback, and it stores
// optional values directly as pointers so that "unset" and "set to zero" remain
// distinguishable.
package workflow

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	common "go.temporal.io/api/common/v1"
	deployment "go.temporal.io/api/deployment/v1"
	enums "go.temporal.io/api/enums/v1"
	taskqueue "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/types/known/durationpb"
)

func nexGenFunctionName[F any](value F) string {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String:
		return rv.String()
	case reflect.Func:
		fullName := runtime.FuncForPC(rv.Pointer()).Name()
		elements := strings.Split(fullName, ".")
		shortName := elements[len(elements)-1]
		return strings.TrimSuffix(shortName, "-fm")
	default:
		panic("nex-gen function name requires string or function")
	}
}

// --- Duration (google.protobuf.Duration) ---

func durationToProto(_ Context, d *time.Duration) (*durationpb.Duration, error) {
	if d == nil {
		return nil, nil
	}
	return durationpb.New(*d), nil
}

func durationFromProto(_ Context, d *durationpb.Duration) (*time.Duration, error) {
	if d == nil {
		return nil, nil
	}
	value := d.AsDuration()
	return &value, nil
}

// --- TaskQueue (temporal.api.taskqueue.v1.TaskQueue) ---

func taskQueueToProto(_ Context, name *string) (*taskqueue.TaskQueue, error) {
	if name == nil {
		return nil, nil
	}
	return &taskqueue.TaskQueue{Name: *name}, nil
}

func taskQueueFromProto(_ Context, tq *taskqueue.TaskQueue) (*string, error) {
	if tq == nil {
		return nil, nil
	}
	value := tq.GetName()
	return &value, nil
}

// --- RetryPolicy (temporal.api.common.v1.RetryPolicy) ---

func retryPolicyToProto(_ Context, p *temporal.RetryPolicy) (*common.RetryPolicy, error) {
	if p == nil {
		return nil, nil
	}
	proto := &common.RetryPolicy{
		BackoffCoefficient:     p.BackoffCoefficient,
		MaximumAttempts:        p.MaximumAttempts,
		NonRetryableErrorTypes: p.NonRetryableErrorTypes,
	}
	if p.InitialInterval != 0 {
		proto.InitialInterval = durationpb.New(p.InitialInterval)
	}
	if p.MaximumInterval != 0 {
		proto.MaximumInterval = durationpb.New(p.MaximumInterval)
	}
	return proto, nil
}

func retryPolicyFromProto(_ Context, p *common.RetryPolicy) (*temporal.RetryPolicy, error) {
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

func priorityToProto(_ Context, p *temporal.Priority) (*common.Priority, error) {
	if p == nil {
		return nil, nil
	}
	return &common.Priority{
		PriorityKey:    int32(p.PriorityKey),
		FairnessKey:    p.FairnessKey,
		FairnessWeight: p.FairnessWeight,
	}, nil
}

func priorityFromProto(_ Context, p *common.Priority) (*temporal.Priority, error) {
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

func workflowTypeToProto(_ Context, name *string) (*common.WorkflowType, error) {
	if name == nil {
		return nil, nil
	}
	return &common.WorkflowType{Name: *name}, nil
}

func workflowTypeFromProto(_ Context, t *common.WorkflowType) (*string, error) {
	if t == nil {
		return nil, nil
	}
	value := t.GetName()
	return &value, nil
}

// --- Payload / Payloads (temporal.api.common.v1.Payload[s]) ---
func payloadToProto(ctx Context, value any) (*common.Payload, error) {
	return nexGenWorkflowDataConverter(ctx).ToPayload(value)
}

func payloadFromProto(ctx Context, payload *common.Payload) (any, error) {
	if payload == nil {
		return nil, nil
	}
	var value any
	if err := nexGenWorkflowDataConverter(ctx).FromPayload(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func payloadsToProto(ctx Context, values []any) (*common.Payloads, error) {
	if len(values) == 0 {
		return nil, nil
	}
	payloads, err := nexGenWorkflowDataConverter(ctx).ToPayloads(values...)
	if err != nil {
		return nil, err
	}
	return payloads, nil
}

func payloadsFromProto(ctx Context, payloads *common.Payloads) ([]any, error) {
	if payloads == nil {
		return nil, nil
	}
	values := make([]any, 0, len(payloads.GetPayloads()))
	for _, payload := range payloads.GetPayloads() {
		value, err := payloadFromProto(ctx, payload)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func nexGenWorkflowDataConverter(ctx Context) converter.DataConverter {
	dataConverter := converter.GetDefaultDataConverter()
	if options := ctx.Value("wfEnvOptions"); options != nil {
		optionsValue := reflect.ValueOf(options)
		if optionsValue.Kind() == reflect.Pointer && !optionsValue.IsNil() {
			optionsValue = optionsValue.Elem()
		}
		if optionsValue.Kind() == reflect.Struct {
			field := optionsValue.FieldByName("DataConverter")
			if field.IsValid() && field.CanInterface() && !field.IsNil() {
				if value, ok := field.Interface().(converter.DataConverter); ok {
					dataConverter = value
				}
			}
		}
	}
	if contextAware, ok := dataConverter.(ContextAware); ok {
		return contextAware.WithWorkflowContext(ctx)
	}
	return dataConverter
}

// --- Memo (temporal.api.common.v1.Memo) ---

func memoToProto(ctx Context, memo map[string]any) (*common.Memo, error) {
	if memo == nil {
		return nil, nil
	}
	fields := make(map[string]*common.Payload, len(memo))
	for key, value := range memo {
		payload, err := payloadToProto(ctx, value)
		if err != nil {
			return nil, fmt.Errorf("encode workflow memo error: %v", err)
		}
		fields[key] = payload
	}
	return &common.Memo{Fields: fields}, nil
}

func memoFromProto(ctx Context, memo *common.Memo) (map[string]any, error) {
	if memo == nil {
		return nil, nil
	}
	result := make(map[string]any, len(memo.GetFields()))
	for key, payload := range memo.GetFields() {
		value, err := payloadFromProto(ctx, payload)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---

func searchAttributesToProto(_ Context, searchAttributes *temporal.SearchAttributes) (*common.SearchAttributes, error) {
	if searchAttributes == nil || searchAttributes.Size() == 0 {
		return nil, nil
	}
	fields := make(map[string]*common.Payload, searchAttributes.Size())
	for key, value := range searchAttributes.GetUntypedValues() {
		payload, err := converter.GetDefaultDataConverter().ToPayload(value)
		if err != nil {
			return nil, fmt.Errorf("encode search attribute [%s] error: %v", key, err)
		}
		if payload.GetData() != nil {
			if payload.Metadata == nil {
				payload.Metadata = map[string][]byte{}
			}
			payload.Metadata["type"] = []byte(key.GetValueType().String())
		}
		fields[key.GetName()] = payload
	}
	return &common.SearchAttributes{IndexedFields: fields}, nil
}

// --- VersioningOverride (temporal.api.workflow.v1.VersioningOverride) ---

func versioningOverrideToProto(_ Context, versioningOverride *client.VersioningOverride) (*workflowpb.VersioningOverride, error) {
	if versioningOverride == nil || *versioningOverride == nil {
		return nil, nil
	}
	switch v := (*versioningOverride).(type) {
	case *client.PinnedVersioningOverride:
		return &workflowpb.VersioningOverride{
			Behavior:      enums.VERSIONING_BEHAVIOR_PINNED,
			PinnedVersion: v.Version.DeploymentName + "." + v.Version.BuildID,
			Deployment: &deployment.Deployment{
				SeriesName: v.Version.DeploymentName,
				BuildId:    v.Version.BuildID,
			},
			Override: &workflowpb.VersioningOverride_Pinned{
				Pinned: &workflowpb.VersioningOverride_PinnedOverride{
					Behavior: workflowpb.VersioningOverride_PINNED_OVERRIDE_BEHAVIOR_PINNED,
					Version: &deployment.WorkerDeploymentVersion{
						DeploymentName: v.Version.DeploymentName,
						BuildId:        v.Version.BuildID,
					},
				},
			},
		}, nil
	case *client.AutoUpgradeVersioningOverride:
		return &workflowpb.VersioningOverride{
			Behavior: enums.VERSIONING_BEHAVIOR_AUTO_UPGRADE,
			Override: &workflowpb.VersioningOverride_AutoUpgrade{
				AutoUpgrade: true,
			},
		}, nil
	default:
		return nil, nil
	}
}

// --- Workflow namespace (sourced field) ---

func workflowNamespace(ctx Context) string {
	if options := ctx.Value("wfEnvOptions"); options != nil {
		optionsValue := reflect.ValueOf(options)
		if optionsValue.Kind() == reflect.Pointer && !optionsValue.IsNil() {
			optionsValue = optionsValue.Elem()
		}
		if optionsValue.Kind() == reflect.Struct {
			field := optionsValue.FieldByName("Namespace")
			if field.IsValid() && field.Kind() == reflect.String {
				return field.String()
			}
		}
	}
	return ""
}

type nexGenNexusOperationFuture struct {
	operation NexusOperationFuture
	result    Future
	execution Future
	get       func(Context, any) error
}

func (f *nexGenNexusOperationFuture) Get(ctx Context, valuePtr any) error {
	if f.get != nil {
		return f.get(ctx, valuePtr)
	}
	return f.result.Get(ctx, valuePtr)
}

func (f *nexGenNexusOperationFuture) IsReady() bool {
	if f.operation != nil {
		return f.operation.IsReady()
	}
	return f.result.IsReady()
}

func (f *nexGenNexusOperationFuture) GetNexusOperationExecution() Future {
	if f.operation != nil {
		return f.operation.GetNexusOperationExecution()
	}
	return f.execution
}

func nexGenFailedNexusOperationFuture(ctx Context, err error) NexusOperationFuture {
	result, resultSettable := NewFuture(ctx)
	resultSettable.SetError(err)
	execution, executionSettable := NewFuture(ctx)
	executionSettable.SetError(err)
	return &nexGenNexusOperationFuture{result: result, execution: execution}
}

func nexGenFutureResultTypeError() error {
	return errors.New("nex-gen future result pointer has unexpected type")
}
