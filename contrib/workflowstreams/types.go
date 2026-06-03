package workflowstreams

import (
	"time"

	commonpb "go.temporal.io/api/common/v1"
)

// Fixed handler names. These are part of the cross-language wire protocol and
// match the Python and TypeScript packages exactly. The Go SDK normally
// reserves the "__temporal_" prefix, but explicitly permits the
// "__temporal_workflow_stream_" sub-namespace for this package.
const (
	// PublishSignalName is the signal external publishers send to append a
	// batch of items to the stream.
	PublishSignalName = "__temporal_workflow_stream_publish"
	// PollUpdateName is the update subscribers send to long-poll for new items.
	PollUpdateName = "__temporal_workflow_stream_poll"
	// OffsetQueryName is the query that returns the current global offset.
	OffsetQueryName = "__temporal_workflow_stream_offset"
)

// Error types surfaced by the poll update and truncate. These match the type
// strings used by the Python and TypeScript packages.
const (
	// ErrTypeTruncatedOffset is the ApplicationError type returned by the poll
	// update when the requested offset has already been truncated.
	ErrTypeTruncatedOffset = "TruncatedOffset"
	// ErrTypeTruncateOutOfRange is the ApplicationError type returned by
	// Truncate when the requested offset is past the end of the log.
	ErrTypeTruncateOutOfRange = "TruncateOutOfRange"
)

// maxPollResponseBytes caps the estimated wire size of a single poll response.
// Responses that would exceed this are truncated and signal MoreReady so the
// subscriber pages through the remainder.
const maxPollResponseBytes = 1_000_000

// Default option values, matching the Python and TypeScript packages.
const (
	defaultBatchInterval    = 2 * time.Second
	defaultPollCooldown     = 100 * time.Millisecond
	defaultPublisherTTL     = 15 * time.Minute
	defaultMaxRetryDuration = 10 * time.Minute
)

// PublishEntry is a single entry within a publish batch on the wire. Data is a
// base64-encoded, marshaled commonpb.Payload.
type PublishEntry struct {
	Topic string `json:"topic"`
	Data  string `json:"data"`
}

// PublishInput is the signal payload carrying a batch of entries to publish,
// along with the dedup fields.
type PublishInput struct {
	Items       []PublishEntry `json:"items"`
	PublisherID string         `json:"publisher_id"`
	Sequence    int64          `json:"sequence"`
}

// PollInput is the update payload: a request to poll for new items.
type PollInput struct {
	Topics     []string `json:"topics"`
	FromOffset int64    `json:"from_offset"`
}

// WireItem is the wire representation of a stream item. Data is a
// base64-encoded, marshaled commonpb.Payload.
type WireItem struct {
	Topic  string `json:"topic"`
	Data   string `json:"data"`
	Offset int64  `json:"offset"`
}

// PollResult is the update response: items matching the poll request. When
// MoreReady is true the response was truncated to stay within size limits and
// the subscriber should poll again immediately rather than applying a cooldown.
type PollResult struct {
	Items      []WireItem `json:"items"`
	NextOffset int64      `json:"next_offset"`
	MoreReady  bool       `json:"more_ready"`
}

// WorkflowStreamState is a serializable snapshot of stream state for
// continue-as-new. Thread a *WorkflowStreamState field through your workflow
// input and pass it to New.
type WorkflowStreamState struct {
	Log               []WireItem         `json:"log"`
	BaseOffset        int64              `json:"base_offset"`
	PublisherSeqs     map[string]int64   `json:"publisher_sequences"`
	PublisherLastSeen map[string]float64 `json:"publisher_last_seen"`
}

// WorkflowStreamItem is a single decoded item yielded by a subscription. Data
// is the raw Payload; decode it at the call site with a payload converter,
// e.g. converter.GetDefaultDataConverter().FromPayload(item.Data, &dst).
type WorkflowStreamItem struct {
	Topic  string
	Data   *commonpb.Payload
	Offset int64
}
