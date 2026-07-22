package workflow

import (
	"fmt"
	"reflect"
	"time"

	common "go.temporal.io/api/common/v1"
	deploymentpb "go.temporal.io/api/deployment/v1"
	enums "go.temporal.io/api/enums/v1"
	taskqueue "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
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
	return &taskqueue.TaskQueue{Name: *name}, nil
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

// --- WorkflowType (temporal.api.common.v1.WorkflowType) ---

func workflowTypeToProto(_ Context, name *string) (*common.WorkflowType, error) {
	if name == nil {
		return nil, nil
	}
	return &common.WorkflowType{Name: *name}, nil
}

// --- Payload / Payloads (temporal.api.common.v1.Payload[s]) ---
func payloadToProto(ctx Context, value any) (*common.Payload, error) {
	return getWorkflowDataConverter(ctx).ToPayload(value)
}

func payloadFromProto(ctx Context, payload *common.Payload) (any, error) {
	if payload == nil {
		return nil, nil
	}
	var value any
	if err := getWorkflowDataConverter(ctx).FromPayload(payload, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func payloadsToProto(ctx Context, values []any) (*common.Payloads, error) {
	return getWorkflowDataConverter(ctx).ToPayloads(values...)
}

func getWorkflowDataConverter(ctx Context) converter.DataConverter {
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

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---

func searchAttributesToProto(_ Context, searchAttributes *temporal.SearchAttributes) (*common.SearchAttributes, error) {
	if searchAttributes == nil {
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
			Deployment: &deploymentpb.Deployment{
				SeriesName: v.Version.DeploymentName,
				BuildId:    v.Version.BuildID,
			},
			Override: &workflowpb.VersioningOverride_Pinned{
				Pinned: &workflowpb.VersioningOverride_PinnedOverride{
					Behavior: workflowpb.VersioningOverride_PINNED_OVERRIDE_BEHAVIOR_PINNED,
					Version: &deploymentpb.WorkerDeploymentVersion{
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
