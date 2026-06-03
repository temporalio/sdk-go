# Workflow Streams

A durable publish/subscribe log hosted inside a Temporal Workflow.

External code (activities, starters, other workflows) publishes messages to
named topics via **signals**; subscribers long-poll for new items via
**updates**; a **query** exposes the current offset. The stream is backed by
Temporal's durable execution, giving ordered, durable, exactly-once delivery
with client-side batching, publisher dedup, continue-as-new survival,
truncation, and ~1 MB response paging.

It is well suited to durable event streams whose cost scales with durable
batches rather than message count. Each poll round-trip costs ~100 ms of
latency, so it is not intended for ultra-low-latency streaming.

## Workflow side

Construct a `WorkflowStream` once at the start of your workflow. The constructor
registers the publish signal, poll update, and offset query handlers.

```go
func MyWorkflow(ctx workflow.Context, priorState *workflowstreams.WorkflowStreamState) error {
	stream, err := workflowstreams.NewStream(ctx, priorState)
	if err != nil {
		return err
	}

	// Optionally publish from workflow code:
	if err := stream.Topic("events").Publish("hello from the workflow"); err != nil {
		return err
	}

	// Run your workflow; the stream serves external publishers and subscribers
	// for as long as the workflow is running.
	return workflow.Await(ctx, func() bool { return done })
}
```

For workflows that support continue-as-new, thread a
`*workflowstreams.WorkflowStreamState` field through your workflow input and
pass it to `NewStream` — it is `nil` on a fresh start and carries accumulated
state across continue-as-new boundaries:

```go
return stream.ContinueAsNew(ctx, MyWorkflow, func(state *workflowstreams.WorkflowStreamState) []any {
	return []any{state}
})
```

## Publishing (client side)

From an activity, use `NewClientFromActivity` to target the parent workflow:

```go
func PublishActivity(ctx context.Context) error {
	c, err := workflowstreams.NewClientFromActivity(ctx, workflowstreams.Options{})
	if err != nil {
		return err
	}
	defer c.Close(ctx)

	topic := c.Topic("events")
	for i := range 100 {
		topic.Publish(fmt.Sprintf("item %d", i), false)
	}
	return nil // Close flushes the remaining buffer
}
```

From a starter or any code with a `client.Client`, use `NewClient` with an
explicit workflow ID:

```go
c := workflowstreams.NewClient(temporalClient, workflowID, workflowstreams.Options{})
defer c.Close(ctx)
c.Topic("events").Publish("from outside", true /* forceFlush */)
```

Items are buffered and flushed automatically every `BatchInterval` (default 2s),
when the buffer reaches `MaxBatchSize`, on `forceFlush`, on an explicit
`Flush`, or on `Close`.

## Subscribing

`Subscribe` returns a range-over-func iterator (Go 1.23+):

```go
for item, err := range c.Subscribe(ctx, workflowstreams.SubscribeOptions{
	Topics: []string{"events"}, // empty/nil = all topics
}) {
	if err != nil {
		return err
	}
	var s string
	if err := converter.GetDefaultDataConverter().FromPayload(item.Data, &s); err != nil {
		return err
	}
	fmt.Printf("offset=%d topic=%s value=%s\n", item.Offset, item.Topic, s)
}
```

Breaking out of the loop or cancelling `ctx` stops the subscription and tears
down the poll loop. The iterator ends cleanly when the workflow reaches a
terminal state, automatically follows continue-as-new chains, and recovers from
truncation by restarting from the current base offset.

Items yield the raw `*commonpb.Payload`; decode at the call site with your data
converter. Offsets are **global** (across all topics), not per-topic.

## Options

| Option | Default | Meaning |
| --- | --- | --- |
| `BatchInterval` | 2s | Automatic flush interval |
| `MaxBatchSize` | unset | Flush once the buffer reaches this size |
| `MaxRetryDuration` | 10m | Max time to retry a failed flush before `FlushTimeoutError`. Must be < the workflow's publisher TTL (15m) to preserve exactly-once delivery |
| `DataConverter` | default | Converter for publishing/decoding |
| `SubscribeOptions.PollCooldown` | 100ms | Min interval between polls |

## Cross-language protocol

The handler names (`PublishSignalName`, `PollUpdateName`, `OffsetQueryName`),
the JSON envelope field names, and the per-item payload encoding (base64 of the
marshaled `temporal.api.common.v1.Payload`) match the Python and TypeScript
packages exactly, so a Go publisher or subscriber interoperates with a
Python/TypeScript workflow and vice versa. The data converter codec chain
(encryption, compression) runs once on the signal/update envelope — never per
item — so payloads are not double-encoded.
