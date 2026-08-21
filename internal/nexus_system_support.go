package internal

import (
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
)

func ConvertToPBRetryPolicy(retryPolicy *RetryPolicy) *commonpb.RetryPolicy {
	return convertToPBRetryPolicy(retryPolicy)
}

func ConvertToPBPriority(priority Priority) *commonpb.Priority {
	return convertToPBPriority(priority)
}

func VersioningOverrideToProto(versioningOverride VersioningOverride) *workflowpb.VersioningOverride {
	return versioningOverrideToProto(versioningOverride)
}

func SerializeSearchAttributes(
	untypedAttributes map[string]interface{},
	typedAttributes SearchAttributes,
) (*commonpb.SearchAttributes, error) {
	return serializeSearchAttributes(untypedAttributes, typedAttributes)
}

func EncodeWorkflowMemo(ctx Context, memo map[string]interface{}) (*commonpb.Memo, error) {
	return getWorkflowMemo(
		memo,
		getDataConverterFromWorkflowContext(ctx),
		getWorkflowEnvironment(ctx).TryUse(SDKFlagMemoUserDCEncode),
	)
}
