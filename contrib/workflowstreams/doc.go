// Package workflowstreams provides a durable publish/subscribe log hosted
// inside a Temporal workflow.
//
// External code (activities, starters, other workflows) publishes messages to
// named topics via signals; subscribers long-poll for new items via updates;
// a query exposes the current offset. The stream is backed by Temporal's
// durable execution, giving exactly-once, ordered, cross-language delivery with
// client-side batching, publisher dedup, continue-as-new survival, truncation,
// and response paging.
//
// # Workflow side
//
// Construct a [WorkflowStream] once at the start of your workflow function. The
// constructor registers the publish signal, poll update, and offset query
// handlers on the current workflow:
//
//	func MyWorkflow(ctx workflow.Context, priorState *workflowstreams.WorkflowStreamState) error {
//		stream, err := workflowstreams.NewStream(ctx, priorState)
//		if err != nil {
//			return err
//		}
//		// Optionally publish from workflow code:
//		_ = stream.Topic("events").Publish("hello")
//		// ... run your workflow; the stream serves external publishers/subscribers.
//		return workflow.Await(ctx, func() bool { return done })
//	}
//
// For workflows that support continue-as-new, thread a
// *[WorkflowStreamState] field through your workflow input and pass it as
// priorState — it is nil on a fresh start and carries accumulated state across
// continue-as-new boundaries.
//
// # Client side
//
// From an activity, starter, or any code with a [client.Client], use a
// [Client] to publish and subscribe:
//
//	c := workflowstreams.NewClient(temporalClient, workflowID, workflowstreams.Options{})
//	defer c.Close(ctx)
//	c.Topic("events").Publish("from outside", false)
//	for item, err := range c.Subscribe(ctx, workflowstreams.SubscribeOptions{Topics: []string{"events"}}) {
//		if err != nil {
//			return err
//		}
//		var s string
//		_ = converter.GetDefaultDataConverter().FromPayload(item.Data, &s)
//	}
//
// # Cross-language protocol
//
// The handler names (see PublishSignalName, PollUpdateName, OffsetQueryName),
// the JSON envelope field names, and the per-item payload encoding (base64 of
// the marshaled temporal.api.common.v1.Payload) match the Python
// (temporalio.contrib.workflow_streams) and TypeScript
// (@temporalio/workflow-streams) packages, so a Go publisher or subscriber
// interoperates with a Python/TypeScript workflow and vice versa.
//
// The data converter codec chain (encryption, compression) runs once on the
// signal/update envelope that carries each batch — not per item — so payloads
// are never double-encoded. Each per-item Payload still carries its encoding
// metadata so consumers can decode it with a payload converter.
package workflowstreams
