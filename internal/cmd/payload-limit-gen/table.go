package main

// The payload-limits decision table: how the SDK mirrors each of the server's payload and memo size
// checks. Every payload-bearing field reachable from a WorkflowService or OperatorService request
// must appear in exactly one list, and every entry must name such a field; either mismatch fails
// `go run . check` and the unit tests.
//
//	blobFields / memoFields  blob / memo limit, warn and error
//	blobWarnFields           blob limit, warn only
//	notValidatedFields       the server enforces no limit the SDK can replicate
//
// To classify a new field:
//
//  1. Look it up in the Java SDK's PayloadLimitValidatorGenerator and the Rust SDK's
//     crates/common/build.rs tables. If it is already classified there, use the same list so the
//     SDKs enforce the same limits.
//  2. Otherwise, find where the server size-checks it: search temporalio/temporal for the field's
//     getter (for example GetSourceContext) next to a size or limit comparison.
//  3. Choose the list from what the server does with an oversized value:
//     - Rejects the request against the namespace blob or memo size limit: blobFields or memoFields.
//     - Accepts the request anyway, for example by truncating a failure: blobWarnFields.
//     - Checks it against a dedicated limit the SDK can't fetch (such as a dynamic config setting),
//     combines it with other fields or with state only the server has, only records a metric, or
//     doesn't check it: notValidatedFields.
//  4. Add it under the comment that gives that reason, or start a new group with its own comment.
//
// The measurement follows from the field's proto type, so the table only records the decision.
var defaultTable = decisionTable{
	blobFields: []string{
		"temporal.api.command.v1.CompleteWorkflowExecutionCommandAttributes.result",
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.input",
		"temporal.api.command.v1.FailWorkflowExecutionCommandAttributes.failure",          // whole Failure proto
		"temporal.api.command.v1.ModifyWorkflowPropertiesCommandAttributes.upserted_memo", // memo data-sum
		"temporal.api.command.v1.RecordMarkerCommandAttributes.details",                   // map<string,Payloads> sum
		"temporal.api.command.v1.ScheduleActivityTaskCommandAttributes.input",
		"temporal.api.command.v1.ScheduleNexusOperationCommandAttributes.input",
		"temporal.api.command.v1.SignalExternalWorkflowExecutionCommandAttributes.input",
		"temporal.api.command.v1.StartChildWorkflowExecutionCommandAttributes.input",
		"temporal.api.command.v1.UpsertWorkflowSearchAttributesCommandAttributes.search_attributes", // indexed_fields data-sum
		// Whole Any body: the server blob-checks proto.Size(body) when processing update messages
		// and fails the workflow task when it's exceeded.
		"temporal.api.protocol.v1.Message.body",
		"temporal.api.query.v1.WorkflowQuery.query_args",
		"temporal.api.workflow.v1.NewWorkflowExecutionInfo.input",
		"temporal.api.workflowservice.v1.RecordActivityTaskHeartbeatByIdRequest.details",
		"temporal.api.workflowservice.v1.RecordActivityTaskHeartbeatRequest.details",
		"temporal.api.workflowservice.v1.RespondActivityTaskCanceledByIdRequest.details",
		"temporal.api.workflowservice.v1.RespondActivityTaskCanceledRequest.details",
		"temporal.api.workflowservice.v1.RespondActivityTaskCompletedByIdRequest.result",
		"temporal.api.workflowservice.v1.RespondActivityTaskCompletedRequest.result",
		"temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest.input",
		"temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest.signal_input",
		"temporal.api.workflowservice.v1.SignalWorkflowExecutionRequest.input",
		"temporal.api.workflowservice.v1.StartActivityExecutionRequest.input",
		"temporal.api.workflowservice.v1.StartNexusOperationExecutionRequest.input",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.input",
	},
	memoFields: []string{
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.memo",
		"temporal.api.command.v1.StartChildWorkflowExecutionCommandAttributes.memo",
		"temporal.api.workflow.v1.NewWorkflowExecutionInfo.memo",
		"temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest.memo",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.memo",
	},
	// Failure responses, which the server truncates rather than rejects, and query results.
	blobWarnFields: []string{
		"temporal.api.workflowservice.v1.RespondActivityTaskFailedByIdRequest.failure",
		"temporal.api.workflowservice.v1.RespondActivityTaskFailedByIdRequest.last_heartbeat_details",
		"temporal.api.workflowservice.v1.RespondActivityTaskFailedRequest.failure",
		"temporal.api.workflowservice.v1.RespondActivityTaskFailedRequest.last_heartbeat_details",
		"temporal.api.workflowservice.v1.RespondNexusTaskFailedRequest.failure",
		"temporal.api.workflowservice.v1.RespondWorkflowTaskFailedRequest.failure",
		"temporal.api.query.v1.WorkflowQueryResult.answer",
		"temporal.api.workflowservice.v1.RespondQueryTaskCompletedRequest.query_result",
	},
	notValidatedFields: []string{
		// Headers: the server records a HeaderSize metric only.
		"temporal.api.batch.v1.BatchOperationSignal.header",
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.header",
		"temporal.api.command.v1.RecordMarkerCommandAttributes.header",
		"temporal.api.command.v1.ScheduleActivityTaskCommandAttributes.header",
		"temporal.api.command.v1.SignalExternalWorkflowExecutionCommandAttributes.header",
		"temporal.api.command.v1.StartChildWorkflowExecutionCommandAttributes.header",
		"temporal.api.query.v1.WorkflowQuery.header",
		"temporal.api.update.v1.Input.header",
		"temporal.api.workflow.v1.NewWorkflowExecutionInfo.header",
		"temporal.api.workflow.v1.PostResetOperation.SignalWorkflow.header",
		"temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest.header",
		"temporal.api.workflowservice.v1.SignalWorkflowExecutionRequest.header",
		"temporal.api.workflowservice.v1.StartActivityExecutionRequest.header",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.header",
		// Search attributes: a separate limit the SDK can't replicate (the server merges with the
		// execution's existing search attributes).
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.search_attributes",
		"temporal.api.command.v1.StartChildWorkflowExecutionCommandAttributes.search_attributes",
		"temporal.api.workflow.v1.NewWorkflowExecutionInfo.search_attributes",
		"temporal.api.workflowservice.v1.CreateScheduleRequest.search_attributes",
		"temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest.search_attributes",
		"temporal.api.workflowservice.v1.StartActivityExecutionRequest.search_attributes",
		"temporal.api.workflowservice.v1.StartNexusOperationExecutionRequest.search_attributes",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.search_attributes",
		"temporal.api.workflowservice.v1.UpdateScheduleRequest.search_attributes",
		// Carry-over fields the SDK doesn't author, or the server doesn't size-check here.
		"temporal.api.command.v1.CancelWorkflowExecutionCommandAttributes.details",
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.failure",
		"temporal.api.command.v1.ContinueAsNewWorkflowExecutionCommandAttributes.last_completion_result",
		"temporal.api.command.v1.RecordMarkerCommandAttributes.failure",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.continued_failure",
		"temporal.api.workflowservice.v1.StartWorkflowExecutionRequest.last_completion_result",
		"temporal.api.workflowservice.v1.TerminateWorkflowExecutionRequest.details",
		// Dedicated limits the SDK can't fetch: UserMetadata (Nexus start only) and the Nexus
		// EndpointSpec description.
		"temporal.api.sdk.v1.UserMetadata.details",
		"temporal.api.sdk.v1.UserMetadata.summary",
		"temporal.api.nexus.v1.EndpointSpec.description",
		// Event group marker label: a dedicated 400-byte server limit, not blob or memo.
		"temporal.api.sdk.v1.EventGroupMarker.Label.label",
		// Update args: the frontend records a metric only; the limit is enforced on delivery via
		// protocol.v1.Message.body.
		"temporal.api.update.v1.Input.args",
		// Query and Nexus failures and the Nexus sync response payload: not size-checked on these
		// paths.
		"temporal.api.nexus.v1.StartOperationResponse.Sync.payload",
		"temporal.api.nexus.v1.StartOperationResponse.failure",
		"temporal.api.query.v1.WorkflowQueryResult.failure",
		"temporal.api.workflowservice.v1.RespondQueryTaskCompletedRequest.failure",
		// Schedules: the server sums memo and action input against the blob limit, a cross-field
		// aggregate this per-field table doesn't express yet.
		"temporal.api.workflowservice.v1.CreateScheduleRequest.memo",
		"temporal.api.workflowservice.v1.UpdateScheduleRequest.memo",
		// Enforced downstream: signal input is blob-checked per target on batch and reset fan-out.
		"temporal.api.batch.v1.BatchOperationSignal.input",
		"temporal.api.workflow.v1.PostResetOperation.SignalWorkflow.input",
		// Not size-checked by the server.
		"temporal.api.batch.v1.BatchOperationTermination.details",
		"temporal.api.deployment.v1.UpdateDeploymentMetadata.upsert_entries",
		"temporal.api.workflowservice.v1.UpdateWorkerDeploymentVersionMetadataRequest.upsert_entries",
		// Cloud compute API: not size-checked by the OSS server.
		"temporal.api.compute.v1.ComputeProvider.details",
		"temporal.api.compute.v1.ComputeScaler.details",
	},
}
