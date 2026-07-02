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
//
// The `package` declaration below is replaced with the generated package name
// when this file is emitted alongside the generated service file.
package support

import (
	"time"

	common "go.temporal.io/api/common/v1"
	taskqueue "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/types/known/durationpb"
)

// --- Duration (google.protobuf.Duration) ---

func DurationToProto(d *time.Duration) *durationpb.Duration {
	if d == nil {
		return nil
	}
	return durationpb.New(*d)
}

func DurationFromProto(d *durationpb.Duration) *time.Duration {
	if d == nil {
		return nil
	}
	value := d.AsDuration()
	return &value
}

// --- TaskQueue (temporal.api.taskqueue.v1.TaskQueue) ---

func TaskQueueToProto(name *string) *taskqueue.TaskQueue {
	if name == nil {
		return nil
	}
	return &taskqueue.TaskQueue{Name: *name}
}

func TaskQueueFromProto(tq *taskqueue.TaskQueue) *string {
	if tq == nil {
		return nil
	}
	value := tq.GetName()
	return &value
}

// --- RetryPolicy (temporal.api.common.v1.RetryPolicy) ---

func RetryPolicyToProto(p *temporal.RetryPolicy) *common.RetryPolicy {
	if p == nil {
		return nil
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
	return proto
}

func RetryPolicyFromProto(p *common.RetryPolicy) *temporal.RetryPolicy {
	if p == nil {
		return nil
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
	return &policy
}

// --- Priority (temporal.api.common.v1.Priority) ---

func PriorityToProto(p *temporal.Priority) *common.Priority {
	if p == nil {
		return nil
	}
	return &common.Priority{
		PriorityKey:    int32(p.PriorityKey),
		FairnessKey:    p.FairnessKey,
		FairnessWeight: p.FairnessWeight,
	}
}

func PriorityFromProto(p *common.Priority) *temporal.Priority {
	if p == nil {
		return nil
	}
	return &temporal.Priority{
		PriorityKey:    int(p.GetPriorityKey()),
		FairnessKey:    p.GetFairnessKey(),
		FairnessWeight: p.GetFairnessWeight(),
	}
}

// --- WorkflowType (temporal.api.common.v1.WorkflowType) ---

func WorkflowTypeToProto(name *string) *common.WorkflowType {
	if name == nil {
		return nil
	}
	return &common.WorkflowType{Name: *name}
}

func WorkflowTypeFromProto(t *common.WorkflowType) *string {
	if t == nil {
		return nil
	}
	value := t.GetName()
	return &value
}

// --- Payload / Payloads (temporal.api.common.v1.Payload[s]) ---
//
// Payload conversion uses the default data converter. When running inside a
// workflow, callers may prefer a converter bound to the workflow's data
// converter; these helpers provide a context-free default suitable for the
// common JSON encoding shared with the Python and TypeScript bindings.

func PayloadToProto(value any) *common.Payload {
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func PayloadFromProto(payload *common.Payload) any {
	if payload == nil {
		return nil
	}
	var value any
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &value); err != nil {
		panic(err)
	}
	return value
}

func PayloadsToProto(values []any) *common.Payloads {
	if len(values) == 0 {
		return nil
	}
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(values...)
	if err != nil {
		panic(err)
	}
	return payloads
}

func PayloadsFromProto(payloads *common.Payloads) []any {
	if payloads == nil {
		return nil
	}
	values := make([]any, 0, len(payloads.GetPayloads()))
	for _, payload := range payloads.GetPayloads() {
		values = append(values, PayloadFromProto(payload))
	}
	return values
}

// --- Memo (temporal.api.common.v1.Memo) ---

func MemoToProto(memo map[string]any) *common.Memo {
	if len(memo) == 0 {
		return nil
	}
	fields := make(map[string]*common.Payload, len(memo))
	for key, value := range memo {
		fields[key] = PayloadToProto(value)
	}
	return &common.Memo{Fields: fields}
}

func MemoFromProto(memo *common.Memo) map[string]any {
	if memo == nil {
		return nil
	}
	result := make(map[string]any, len(memo.GetFields()))
	for key, payload := range memo.GetFields() {
		result[key] = PayloadFromProto(payload)
	}
	return result
}

// --- SearchAttributes (temporal.api.common.v1.SearchAttributes) ---
//
// Search-attribute encoding is deferred: typed search attributes require the
// SDK's search-attribute encoder. This placeholder keeps the bindings
// compiling; populate it when search-attribute support lands.

func SearchAttributesToProto(_ *string) *common.SearchAttributes {
	return nil
}

// --- VersioningOverride (temporal.api.workflow.v1.VersioningOverride) ---
//
// Versioning-override encoding is deferred. This placeholder keeps the bindings
// compiling; populate it when versioning-override support lands.

func VersioningOverrideToProto(_ *client.VersioningOverride) *workflowpb.VersioningOverride {
	return nil
}

// --- Workflow namespace (sourced field) ---
//
// The namespace is sourced at runtime from the workflow context. Conversion in
// a context-free `ToProto` cannot access it, so this returns the empty string;
// the server fills in the caller's namespace. Populate this when sourced fields
// gain access to the workflow context.

func WorkflowNamespace() string {
	return ""
}
