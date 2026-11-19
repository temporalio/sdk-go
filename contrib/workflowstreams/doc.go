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
//	type MyInput struct {
//		ItemsProcessed int // your own workflow state
//		StreamState    *workflowstreams.WorkflowStreamState
//	}
//
//	func MyWorkflow(ctx workflow.Context, input MyInput) error {
//		stream, err := workflowstreams.NewWorkflowStream(ctx, input.StreamState)
//		if err != nil {
//			return err
//		}
//		// Optionally publish from workflow code:
//		_ = stream.Topic("events").Publish("hello")
//		// ... run your workflow; the stream serves external publishers/subscribers.
//		// Block until your workflow's exit condition is met (here, a done flag
//		// set elsewhere, e.g. by a signal).
//		return workflow.Await(ctx, func() bool { return done })
//	}
//
// Continue-as-new starts a fresh run with an empty history, so the stream's log
// and offsets must be carried across each boundary. This is a round-trip: when
// rolling over, return [WorkflowStream.NewContinueAsNewError] instead of a plain
// workflow.NewContinueAsNewError. It snapshots the stream state and hands it to
// your callback, which builds the next run's arguments — carry your own state
// forward alongside the captured state:
//
//	return stream.NewContinueAsNewError(ctx, MyWorkflow, func(state *workflowstreams.WorkflowStreamState) []any {
//		return []any{MyInput{ItemsProcessed: itemsProcessed, StreamState: state}}
//	})
//
// On the next run that captured state arrives as MyInput.StreamState, the value
// passed to [NewWorkflowStream] above: nil on a fresh start, non-nil after a
// roll-over, so the constructor rehydrates the log automatically.
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
// the marshaled temporal.api.common.v1.Payload) match workflow streams
// packages in other languages for interoperability.
//
// The data converter codec chain (encryption, compression) runs once on the
// signal/update envelope that carries each batch — not per item — so payloads
// are never double-encoded. Each per-item Payload still carries its encoding
// metadata so consumers can decode it with a payload converter.
package workflowstreams
