package internal

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/api/temporalproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	systemnexus "go.temporal.io/api/workflowservice/v1/workflowservicenexus/json"

	"go.temporal.io/sdk/converter"
)

func prepareSystemNexusSignalWithStartOperationParams(
	ctx Context,
	env WorkflowEnvironment,
	workflowID string,
	signalName string,
	signalArg interface{},
	options StartWorkflowOptions,
	workflowType string,
	workflowArgs []interface{},
) (executeNexusOperationParams, error) {
	outerPayload, err := newSystemNexusSignalWithStartPayloadFromWorkflow(
		ctx,
		env,
		workflowID,
		signalName,
		signalArg,
		options,
		workflowType,
		workflowArgs,
	)
	if err != nil {
		return executeNexusOperationParams{}, err
	}

	return executeNexusOperationParams{
		client: nexusClient{
			endpoint: systemNexusEndpoint,
			service:  systemnexus.WorkflowService.ServiceName,
		},
		operation:   systemnexus.WorkflowService.SignalWithStartWorkflowExecution.Name(),
		input:       outerPayload,
		options:     NexusOperationOptions{CancellationType: NexusOperationCancellationTypeWaitCompleted},
		nexusHeader: nexus.Header{},
	}, nil
}

func newSystemNexusSignalWithStartPayloadFromWorkflow(
	ctx Context,
	env WorkflowEnvironment,
	workflowID string,
	signalName string,
	signalArg interface{},
	options StartWorkflowOptions,
	workflowType string,
	workflowArgs []interface{},
) (*commonpb.Payload, error) {
	if workflowID == "" {
		return nil, errWorkflowIDNotSet
	}

	workflowOptionsFromCtx := getWorkflowEnvOptions(ctx)
	dc := WithWorkflowContext(ctx, workflowOptionsFromCtx.DataConverter)
	wfType, input, err := getValidatedWorkflowFunction(workflowType, workflowArgs, dc, env.GetRegistry())
	if err != nil {
		return nil, err
	}

	signalInput, err := encodeArg(dc, signalArg)
	if err != nil {
		return nil, err
	}

	header, err := workflowHeaderPropagated(ctx, workflowOptionsFromCtx.ContextPropagators)
	if err != nil {
		return nil, err
	}

	memo, err := getWorkflowMemo(options.Memo, dc, env.TryUse(SDKFlagMemoUserDCEncode))
	if err != nil {
		return nil, err
	}

	searchAttr, err := serializeSearchAttributes(options.SearchAttributes, options.TypedSearchAttributes)
	if err != nil {
		return nil, err
	}

	userMetadata, err := buildUserMetadata(options.StaticSummary, options.StaticDetails, dc)
	if err != nil {
		return nil, err
	}
	var userMetadataMessage proto.Message
	if userMetadata != nil {
		userMetadataMessage = userMetadata
	}

	return newSystemNexusSignalWithStartPayload(
		workflowOptionsFromCtx.Namespace,
		options.requestID,
		workflowID,
		signalName,
		wfType,
		input,
		signalInput,
		header,
		memo,
		searchAttr,
		userMetadataMessage,
		options,
	)
}

func newSystemNexusSignalWithStartInput(
	namespace string,
	requestID string,
	workflowID string,
	signalName string,
	workflowType *WorkflowType,
	input *commonpb.Payloads,
	signalInput *commonpb.Payloads,
	header *commonpb.Header,
	memo *commonpb.Memo,
	searchAttr *commonpb.SearchAttributes,
	userMetadata proto.Message,
	options StartWorkflowOptions,
) (systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput, error) {
	req := systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{
		Namespace:  namespace,
		RequestID:  requestID,
		WorkflowID: workflowID,
		SignalName: signalName,
		TaskQueue:  toSystemNexusTaskQueue(options.TaskQueue),
		WorkflowType: &systemnexus.WorkflowType{
			Name: workflowType.Name,
		},
		WorkflowIDConflictPolicy: toSystemNexusWorkflowIDConflictPolicy(options.WorkflowIDConflictPolicy),
		WorkflowIDReusePolicy:    toSystemNexusWorkflowIDReusePolicy(options.WorkflowIDReusePolicy),
		VersioningOverride:       toSystemNexusVersioningOverride(options.VersioningOverride),
		Priority:                 toSystemNexusPriority(options.Priority),
	}
	var err error
	if req.RetryPolicy, err = toSystemNexusRetryPolicy(options.RetryPolicy); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}

	if req.Input, err = toSystemNexusInput(input); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}
	if req.SignalInput, err = toSystemNexusInput(signalInput); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}
	if req.Header, err = toSystemNexusHeader(header); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}
	if req.Memo, err = toSystemNexusMemo(memo); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}
	if req.SearchAttributes, err = toSystemNexusSearchAttributes(searchAttr); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}
	if req.UserMetadata, err = toSystemNexusUserMetadata(userMetadata); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	}

	if executionTimeout, err := toSystemNexusDurationString(options.WorkflowExecutionTimeout); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	} else {
		req.WorkflowExecutionTimeout = executionTimeout
	}
	if runTimeout, err := toSystemNexusDurationString(options.WorkflowRunTimeout); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	} else {
		req.WorkflowRunTimeout = runTimeout
	}
	if taskTimeout, err := toSystemNexusDurationString(options.WorkflowTaskTimeout); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	} else {
		req.WorkflowTaskTimeout = taskTimeout
	}
	if startDelay, err := toSystemNexusDurationString(options.StartDelay); err != nil {
		return systemnexus.WorkflowServiceSignalWithStartWorkflowExecutionInput{}, err
	} else {
		req.WorkflowStartDelay = startDelay
	}

	req.CronSchedule = options.CronSchedule
	return req, nil
}

func newSystemNexusSignalWithStartPayload(
	namespace string,
	requestID string,
	workflowID string,
	signalName string,
	workflowType *WorkflowType,
	input *commonpb.Payloads,
	signalInput *commonpb.Payloads,
	header *commonpb.Header,
	memo *commonpb.Memo,
	searchAttr *commonpb.SearchAttributes,
	userMetadata proto.Message,
	options StartWorkflowOptions,
) (*commonpb.Payload, error) {
	req, err := newSystemNexusSignalWithStartInput(
		namespace,
		requestID,
		workflowID,
		signalName,
		workflowType,
		input,
		signalInput,
		header,
		memo,
		searchAttr,
		userMetadata,
		options,
	)
	if err != nil {
		return nil, err
	}
	return converter.GetDefaultDataConverter().ToPayload(req)
}

func toSystemNexusInput(payloads *commonpb.Payloads) (*systemnexus.Input, error) {
	if payloads == nil || len(payloads.Payloads) == 0 {
		return nil, nil
	}
	items, err := toSystemNexusPayloadValues(payloads.Payloads)
	if err != nil {
		return nil, err
	}
	return &systemnexus.Input{Payloads: items}, nil
}

func toSystemNexusPayloadValues(payloads []*commonpb.Payload) ([]any, error) {
	items := make([]any, len(payloads))
	for i, payload := range payloads {
		value, err := systemNexusProtoToJSONValue(payload)
		if err != nil {
			return nil, err
		}
		items[i] = value
	}
	return items, nil
}

func toSystemNexusHeader(header *commonpb.Header) (*systemnexus.Header, error) {
	if header == nil {
		return nil, nil
	}
	value, err := systemNexusProtoToJSONValue(header)
	if err != nil {
		return nil, err
	}
	valueMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("system nexus header JSON must be an object")
	}
	fields, _ := valueMap["fields"].(map[string]any)
	return &systemnexus.Header{Fields: fields}, nil
}

func toSystemNexusMemo(memo *commonpb.Memo) (*systemnexus.Memo, error) {
	if memo == nil {
		return nil, nil
	}
	value, err := systemNexusProtoToJSONValue(memo)
	if err != nil {
		return nil, err
	}
	valueMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("system nexus memo JSON must be an object")
	}
	fields, _ := valueMap["fields"].(map[string]any)
	return &systemnexus.Memo{Fields: fields}, nil
}

func toSystemNexusSearchAttributes(searchAttr *commonpb.SearchAttributes) (*systemnexus.SearchAttributes, error) {
	if searchAttr == nil {
		return nil, nil
	}
	value, err := systemNexusProtoToJSONValue(searchAttr)
	if err != nil {
		return nil, err
	}
	valueMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("system nexus search attributes JSON must be an object")
	}
	fields, _ := valueMap["indexedFields"].(map[string]any)
	return &systemnexus.SearchAttributes{IndexedFields: fields}, nil
}

func toSystemNexusUserMetadata(userMetadata proto.Message) (*systemnexus.UserMetadata, error) {
	if userMetadata == nil {
		return nil, nil
	}
	value, err := systemNexusProtoToJSONValue(userMetadata)
	if err != nil {
		return nil, err
	}
	valueMap, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("system nexus user metadata JSON must be an object")
	}
	return &systemnexus.UserMetadata{
		Summary: valueMap["summary"],
		Details: valueMap["details"],
	}, nil
}

func systemNexusProtoToJSONValue(message proto.Message) (any, error) {
	data, err := temporalproto.CustomJSONMarshalOptions{
		Metadata: map[string]interface{}{
			commonpb.EnablePayloadShorthandMetadataKey: true,
		},
	}.Marshal(message)
	if err != nil {
		return nil, err
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func toSystemNexusDurationString(d time.Duration) (string, error) {
	if d == 0 {
		return "", nil
	}
	value, err := systemNexusProtoToJSONValue(durationpb.New(d))
	if err != nil {
		return "", err
	}
	durationValue, ok := value.(string)
	if !ok {
		return "", errors.New("system nexus duration JSON must be a string")
	}
	return durationValue, nil
}

func toSystemNexusTaskQueue(name string) *systemnexus.TaskQueue {
	if name == "" {
		return nil
	}
	kind := systemnexus.TaskQueueKindNormal
	return &systemnexus.TaskQueue{Name: name, Kind: &kind}
}

func toSystemNexusRetryPolicy(retryPolicy *RetryPolicy) (*systemnexus.RetryPolicy, error) {
	if retryPolicy == nil {
		return nil, nil
	}
	policy := &systemnexus.RetryPolicy{
		BackoffCoefficient:     retryPolicy.BackoffCoefficient,
		MaximumAttempts:        int64(retryPolicy.MaximumAttempts),
		NonRetryableErrorTypes: retryPolicy.NonRetryableErrorTypes,
	}
	if retryPolicy.InitialInterval != 0 {
		initialInterval, err := toSystemNexusDurationString(retryPolicy.InitialInterval)
		if err != nil {
			return nil, err
		}
		policy.InitialInterval = initialInterval
	}
	if retryPolicy.MaximumInterval != 0 {
		maximumInterval, err := toSystemNexusDurationString(retryPolicy.MaximumInterval)
		if err != nil {
			return nil, err
		}
		policy.MaximumInterval = maximumInterval
	}
	return policy, nil
}

func toSystemNexusWorkflowIDConflictPolicy(policy enumspb.WorkflowIdConflictPolicy) *systemnexus.WorkflowIDConflictPolicy {
	switch policy {
	case enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL:
		value := systemnexus.WorkflowIDConflictPolicyFail
		return &value
	case enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING:
		value := systemnexus.WorkflowIDConflictPolicyUseExisting
		return &value
	case enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING:
		value := systemnexus.WorkflowIDConflictPolicyTerminateExisting
		return &value
	default:
		return nil
	}
}

func toSystemNexusWorkflowIDReusePolicy(policy enumspb.WorkflowIdReusePolicy) *systemnexus.WorkflowIDReusePolicy {
	switch policy {
	case enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE:
		value := systemnexus.WorkflowIDReusePolicyAllowDuplicate
		return &value
	case enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY:
		value := systemnexus.WorkflowIDReusePolicyAllowDuplicateFailedOnly
		return &value
	case enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE:
		value := systemnexus.WorkflowIDReusePolicyRejectDuplicate
		return &value
	case enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING:
		value := systemnexus.WorkflowIDReusePolicyTerminateIfRunning
		return &value
	default:
		return nil
	}
}

func toSystemNexusVersioningOverride(versioningOverride VersioningOverride) *systemnexus.VersioningOverride {
	if versioningOverride == nil {
		return nil
	}
	switch v := versioningOverride.(type) {
	case *PinnedVersioningOverride:
		behavior := systemnexus.VersioningBehaviorPinned
		pinnedBehavior := systemnexus.PinnedOverrideBehaviorPinned
		return &systemnexus.VersioningOverride{
			Behavior:      &behavior,
			PinnedVersion: v.Version.toCanonicalString(),
			Deployment: &systemnexus.Deployment{
				SeriesName: v.Version.DeploymentName,
				BuildID:    v.Version.BuildID,
			},
			Pinned: &systemnexus.Pinned{
				Behavior: &pinnedBehavior,
				Version: &systemnexus.Version{
					DeploymentName: v.Version.DeploymentName,
					BuildID:        v.Version.BuildID,
				},
			},
		}
	case *AutoUpgradeVersioningOverride:
		behavior := systemnexus.VersioningBehaviorAutoUpgrade
		return &systemnexus.VersioningOverride{
			Behavior:    &behavior,
			AutoUpgrade: true,
		}
	default:
		return nil
	}
}

func toSystemNexusPriority(priority Priority) *systemnexus.Priority {
	var defaultPriority Priority
	if priority == defaultPriority {
		return nil
	}
	return &systemnexus.Priority{
		PriorityKey:    int64(priority.PriorityKey),
		FairnessKey:    priority.FairnessKey,
		FairnessWeight: float64(priority.FairnessWeight),
	}
}
