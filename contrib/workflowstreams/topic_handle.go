package workflowstreams

import (
	"context"
	"iter"
)

// TopicHandle publishes to and subscribes from a single topic.
type TopicHandle struct {
	name   string
	client *Client
}

// Name returns the topic name.
func (h *TopicHandle) Name() string { return h.name }

// Publish buffers value for publishing on this topic. value goes through the
// client's data converter at flush time; a pre-built *commonpb.Payload bypasses
// conversion. Pass forceFlush to wake the publisher and send immediately.
func (h *TopicHandle) Publish(value any, forceFlush bool) {
	h.client.pub.publish(h.name, value, forceFlush)
}

// Subscribe returns an iterator over items on this topic, starting at
// fromOffset. See Client.Subscribe.
func (h *TopicHandle) Subscribe(ctx context.Context, fromOffset int64) iter.Seq2[WorkflowStreamItem, error] {
	return h.client.Subscribe(ctx, SubscribeOptions{Topics: []string{h.name}, FromOffset: fromOffset})
}
