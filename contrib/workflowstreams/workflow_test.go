package workflowstreams

import (
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
	_, err := NewStream(ctx, priorState)
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
