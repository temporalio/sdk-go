package workflowstreams

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// streamHostWorkflow hosts a WorkflowStream and runs until it receives a
// "finish" signal. priorState may be nil.
func streamHostWorkflow(ctx workflow.Context, priorState *WorkflowStreamState) error {
	_, err := NewWorkflowStream(ctx, priorState)
	if err != nil {
		return err
	}

	finished := false
	workflow.Go(ctx, func(ctx workflow.Context) {
		workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
		finished = true
	})

	return workflow.Await(ctx, func() bool { return finished })
}

type topicVal struct {
	topic string
	value any
}

func mustPublishInput(t *testing.T, publisherID string, seq int64, entries ...topicVal) PublishInput {
	t.Helper()
	in := PublishInput{PublisherID: publisherID, Sequence: seq}
	for _, e := range entries {
		payload, err := converter.GetDefaultDataConverter().ToPayload(e.value)
		require.NoError(t, err)
		data, err := encodePayloadWire(payload)
		require.NoError(t, err)
		in.Items = append(in.Items, PublishEntry{Topic: e.topic, Data: data})
	}
	return in
}

// byteOnlyPublishWorkflow restricts the stream to the byte-slice converter and
// returns whether publishing a string failed — it should, since that set has no
// converter for strings, whereas the default set's JSON fallback would accept
// it. A []byte must still publish cleanly.
func byteOnlyPublishWorkflow(ctx workflow.Context) (bool, error) {
	stream, err := NewWorkflowStream(ctx, nil, WithPayloadConverters(converter.NewByteSlicePayloadConverter()))
	if err != nil {
		return false, err
	}
	if err := stream.Topic("events").Publish([]byte("hi")); err != nil {
		return false, err
	}
	return stream.Topic("events").Publish("not-bytes") != nil, nil
}

func TestWorkflowPublishUsesConfiguredConverters(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(byteOnlyPublishWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var stringPublishFailed bool
	require.NoError(t, env.GetWorkflowResult(&stringPublishFailed))
	require.True(t, stringPublishFailed,
		"a string is unconvertible under the byte-slice-only set, proving WithPayloadConverters drives conversion")
}

func TestExternalPublishAndOffsetQuery(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var offset int64
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(PublishSignalName, mustPublishInput(t, "pub1", 1,
			topicVal{"events", "a"}, topicVal{"events", "b"}))
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(OffsetQueryName)
		require.NoError(t, err)
		require.NoError(t, val.Get(&offset))
	}, 2*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("finish", nil)
	}, 3*time.Millisecond)

	env.ExecuteWorkflow(streamHostWorkflow, (*WorkflowStreamState)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.EqualValues(t, 2, offset)
}

func TestPublisherDedup(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var offset int64
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(PublishSignalName, mustPublishInput(t, "pub1", 1, topicVal{"events", "a"}))
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		// Same publisher + sequence: must be dropped.
		env.SignalWorkflow(PublishSignalName, mustPublishInput(t, "pub1", 1, topicVal{"events", "dup"}))
	}, 2*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(PublishSignalName, mustPublishInput(t, "pub1", 2, topicVal{"events", "c"}))
	}, 3*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		val, err := env.QueryWorkflow(OffsetQueryName)
		require.NoError(t, err)
		require.NoError(t, val.Get(&offset))
	}, 4*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("finish", nil)
	}, 5*time.Millisecond)

	env.ExecuteWorkflow(streamHostWorkflow, (*WorkflowStreamState)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.EqualValues(t, 2, offset, "duplicate batch should be dropped")
}

func TestPollReturnsItemsWithTopicFilter(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	var result PollResult
	var pollErr error
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(PublishSignalName, mustPublishInput(t, "pub1", 1,
			topicVal{"a", "1"}, topicVal{"b", "2"}, topicVal{"a", "3"}))
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(PollUpdateName, "poll1", &testsuite.TestUpdateCallback{
			OnAccept: func() {},
			OnReject: func(err error) { pollErr = err },
			OnComplete: func(success any, err error) {
				if err != nil {
					pollErr = err
					return
				}
				result = success.(PollResult)
			},
		}, PollInput{Topics: []string{"a"}, FromOffset: 0})
	}, 2*time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("finish", nil)
	}, 3*time.Millisecond)

	env.ExecuteWorkflow(streamHostWorkflow, (*WorkflowStreamState)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, pollErr)

	// Only topic "a" items, with global offsets 0 and 2.
	require.Len(t, result.Items, 2)
	require.Equal(t, "a", result.Items[0].Topic)
	require.EqualValues(t, 0, result.Items[0].Offset)
	require.Equal(t, "a", result.Items[1].Topic)
	require.EqualValues(t, 2, result.Items[1].Offset)
	require.EqualValues(t, 3, result.NextOffset)
	require.False(t, result.MoreReady)

	payload, err := decodePayloadWire(result.Items[1].Data)
	require.NoError(t, err)
	var v string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(payload, &v))
	require.Equal(t, "3", v)
}

func TestPollQueryAfterClosure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		stream, err := NewWorkflowStream(ctx, nil)
		if err != nil {
			return err
		}
		for _, topic := range []string{"a", "b", "a"} {
			if err := stream.Topic(topic).Publish(topic); err != nil {
				return err
			}
		}
		if err := stream.Truncate(1); err != nil {
			return err
		}
		stream.DetachPollers()
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	for _, tc := range []struct {
		name    string
		input   PollInput
		offsets []int64
	}{
		{"all", PollInput{}, []int64{1, 2}},
		{"stale", PollInput{FromOffset: -1}, []int64{1, 2}},
		{"filtered", PollInput{Topics: []string{"a"}}, []int64{2}},
		{"unmatched", PollInput{Topics: []string{"missing"}}, nil},
		{"resumed", PollInput{FromOffset: 2}, []int64{2}},
		{"exhausted", PollInput{FromOffset: 3}, nil},
		{"future", PollInput{FromOffset: 100}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := env.QueryWorkflow(PollQueryName, tc.input)
			require.NoError(t, err)
			var result PollResult
			require.NoError(t, value.Get(&result))
			var offsets []int64
			for _, item := range result.Items {
				offsets = append(offsets, item.Offset)
			}
			require.Equal(t, tc.offsets, offsets)
			require.EqualValues(t, 3, result.NextOffset)
			require.False(t, result.MoreReady)
		})
	}
}

func TestPollQueryPagesWithoutMutatingState(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		stream, err := NewWorkflowStream(ctx, nil)
		if err != nil {
			return err
		}
		for range 3 {
			if err := stream.Topic("events").Publish(strings.Repeat("x", 400_000)); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	for page := range 3 {
		value, err := env.QueryWorkflow(PollQueryName, PollInput{FromOffset: int64(page)})
		require.NoError(t, err)
		var result PollResult
		require.NoError(t, value.Get(&result))
		require.Len(t, result.Items, 1)
		require.EqualValues(t, page, result.Items[0].Offset)
		require.EqualValues(t, page+1, result.NextOffset)
		require.Equal(t, page < 2, result.MoreReady)
	}
	value, err := env.QueryWorkflow(PollQueryName, PollInput{})
	require.NoError(t, err)
	var result PollResult
	require.NoError(t, value.Get(&result))
	require.EqualValues(t, 0, result.Items[0].Offset)
}
