package workflowstreams

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

// pollStep is a single scripted reply to one Subscribe poll. updateErr fails the
// UpdateWorkflow call itself; getErr fails the handle.Get; otherwise result is
// returned. runID is the run the update is admitted to, surfaced via the handle's
// RunID() so Subscribe can describe that specific run on failure.
type pollStep struct {
	runID     string
	updateErr error
	getErr    error
	result    PollResult
}

// errStepsExhausted is returned once a fake runs out of scripted poll steps. The
// default describe status (COMPLETED) makes the loop treat it as a clean,
// terminal end, so a test that forgets to break can't spin forever.
var errStepsExhausted = errors.New("fake: poll steps exhausted")

// fakeSubClient scripts UpdateWorkflow/DescribeWorkflowExecution responses so the
// Subscribe polling loop can be exercised without a server. Embedding
// client.Client satisfies the rest of the interface; the unused methods panic if
// called, which the subscribe tests never do.
type fakeSubClient struct {
	client.Client

	mu    sync.Mutex
	steps []pollStep
	idx   int
	polls []PollInput // PollInput captured per UpdateWorkflow call, in order

	// describeByRun maps a run id to the status DescribeWorkflowExecution reports
	// for it. A run not present defaults to COMPLETED, so loops terminate cleanly.
	describeByRun map[string]enumspb.WorkflowExecutionStatus
	describeRuns  []string // run ids passed to DescribeWorkflowExecution, in order
	describeErr   error
}

func (f *fakeSubClient) UpdateWorkflow(_ context.Context, options client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls = append(f.polls, options.Args[0].(PollInput))
	if f.idx >= len(f.steps) {
		return nil, errStepsExhausted
	}
	step := f.steps[f.idx]
	f.idx++
	if step.updateErr != nil {
		return nil, step.updateErr
	}
	return &fakeUpdateHandle{runID: step.runID, result: step.result, err: step.getErr}, nil
}

func (f *fakeSubClient) DescribeWorkflowExecution(_ context.Context, _, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.describeRuns = append(f.describeRuns, runID)
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	status, ok := f.describeByRun[runID]
	if !ok {
		status = enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{Status: status},
	}, nil
}

func (f *fakeSubClient) recordedPolls() []PollInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PollInput(nil), f.polls...)
}

func (f *fakeSubClient) recordedDescribeRuns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.describeRuns...)
}

// fakeUpdateHandle returns a scripted PollResult (or error) from Get and reports
// its run id via RunID(). The remaining WorkflowUpdateHandle methods are unused by
// Subscribe and panic via the nil embedded interface if called.
type fakeUpdateHandle struct {
	client.WorkflowUpdateHandle
	runID  string
	result PollResult
	err    error
}

func (h *fakeUpdateHandle) RunID() string { return h.runID }

func (h *fakeUpdateHandle) Get(_ context.Context, valuePtr interface{}) error {
	if h.err != nil {
		return h.err
	}
	if p, ok := valuePtr.(*PollResult); ok {
		*p = h.result
	}
	return nil
}

// newSubClient builds a Client wired to fc directly, bypassing NewClient so no
// background publisher is started.
func newSubClient(fc *fakeSubClient) *Client {
	return &Client{c: fc, workflowID: "wf", followCAN: true}
}

// wireItem encodes value into a WireItem the way a workflow would.
func wireItem(t *testing.T, topic, value string, offset int64) WireItem {
	t.Helper()
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)
	enc, err := encodePayloadWire(payload)
	require.NoError(t, err)
	return WireItem{Topic: topic, Data: enc, Offset: offset}
}

func decodeItem(t *testing.T, p *commonpb.Payload) string {
	t.Helper()
	var s string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(p, &s))
	return s
}

func TestSubscribeDeliversItemsAndAdvancesOffset(t *testing.T) {
	fc := &fakeSubClient{steps: []pollStep{
		{result: PollResult{Items: []WireItem{wireItem(t, "evt", "a", 1)}, NextOffset: 2, MoreReady: true}},
		{result: PollResult{Items: []WireItem{wireItem(t, "evt", "b", 2)}, NextOffset: 3, MoreReady: true}},
	}}
	c := newSubClient(fc)

	var got []string
	var gotOffsets []int64
	for item, err := range c.Subscribe(context.Background(), SubscribeOptions{FromOffset: 1}) {
		require.NoError(t, err)
		got = append(got, decodeItem(t, item.Data))
		gotOffsets = append(gotOffsets, item.Offset)
		if len(got) == 2 {
			break
		}
	}

	require.Equal(t, []string{"a", "b"}, got)
	require.Equal(t, []int64{1, 2}, gotOffsets)

	polls := fc.recordedPolls()
	require.GreaterOrEqual(t, len(polls), 2)
	require.EqualValues(t, 1, polls[0].FromOffset, "first poll uses the requested offset")
	require.EqualValues(t, 2, polls[1].FromOffset, "second poll advances to the prior NextOffset")
}

func TestSubscribeTruncationResetsOffset(t *testing.T) {
	truncated := temporal.NewNonRetryableApplicationError("truncated", ErrTypeTruncatedOffset, nil)
	fc := &fakeSubClient{steps: []pollStep{
		{getErr: truncated},
		{result: PollResult{Items: []WireItem{wireItem(t, "evt", "a", 0)}, NextOffset: 1, MoreReady: true}},
	}}
	c := newSubClient(fc)

	var got []string
	for item, err := range c.Subscribe(context.Background(), SubscribeOptions{FromOffset: 5}) {
		require.NoError(t, err)
		got = append(got, decodeItem(t, item.Data))
		break
	}

	require.Equal(t, []string{"a"}, got)

	polls := fc.recordedPolls()
	require.GreaterOrEqual(t, len(polls), 2)
	require.EqualValues(t, 5, polls[0].FromOffset, "first poll uses the requested offset")
	require.EqualValues(t, 0, polls[1].FromOffset, "truncation restarts from the beginning")
	require.Empty(t, fc.recordedDescribeRuns(), "truncation is handled without describing the workflow")
}

func TestSubscribeTerminalEndsCleanly(t *testing.T) {
	fc := &fakeSubClient{
		steps: []pollStep{{runID: "R1", getErr: errors.New("workflow gone")}},
		describeByRun: map[string]enumspb.WorkflowExecutionStatus{
			// The polled run itself ended — not a rollover.
			"R1": enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		},
	}
	c := newSubClient(fc)

	var yields int
	for _, err := range c.Subscribe(context.Background(), SubscribeOptions{}) {
		yields++
		require.NoError(t, err, "terminal workflow should end the stream without surfacing an error")
	}
	require.Zero(t, yields, "no items and no error are yielded on a clean terminal end")
	require.Equal(t, []string{"R1"}, fc.recordedDescribeRuns()[:1], "the polled run is the one described")
}

func TestSubscribeContinueAsNewRetries(t *testing.T) {
	// The poll on run R1 fails because R1 continued-as-new mid-poll; describing R1
	// reports CONTINUED_AS_NEW, so Subscribe retries and the next poll lands on the
	// successor run R2.
	fc := &fakeSubClient{
		steps: []pollStep{
			{runID: "R1", getErr: errors.New("update lost to continue-as-new")},
			{runID: "R2", result: PollResult{Items: []WireItem{wireItem(t, "evt", "after-can", 1)}, NextOffset: 2, MoreReady: true}},
		},
		describeByRun: map[string]enumspb.WorkflowExecutionStatus{
			"R1": enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW,
		},
	}
	c := newSubClient(fc)

	var got []string
	for item, err := range c.Subscribe(context.Background(), SubscribeOptions{}) {
		require.NoError(t, err)
		got = append(got, decodeItem(t, item.Data))
		break
	}

	require.Equal(t, []string{"after-can"}, got)
	require.GreaterOrEqual(t, len(fc.recordedPolls()), 2, "the poll is retried after following continue-as-new")
	require.Equal(t, []string{"R1"}, fc.recordedDescribeRuns(), "the rollover is detected by describing the polled run R1, not the latest run")
}

func TestSubscribeRetriesWhileDraining(t *testing.T) {
	// While the workflow is detaching for continue-as-new, the poll validator
	// rejects with ErrTypeStreamDraining. Subscribe must back off and retry
	// rather than surface the rejection; the next poll then lands on the
	// successor run.
	draining := temporal.NewApplicationError("workflow is draining", ErrTypeStreamDraining)
	fc := &fakeSubClient{steps: []pollStep{
		{runID: "R1", getErr: draining},
		{runID: "R2", result: PollResult{Items: []WireItem{wireItem(t, "evt", "after-can", 1)}, NextOffset: 2, MoreReady: true}},
	}}
	c := newSubClient(fc)

	var got []string
	var gotErr error
	for item, err := range c.Subscribe(context.Background(), SubscribeOptions{PollCooldown: time.Millisecond}) {
		if err != nil {
			gotErr = err
			break
		}
		got = append(got, decodeItem(t, item.Data))
		break
	}

	require.NoError(t, gotErr, "a draining rejection must not surface as an error")
	require.Equal(t, []string{"after-can"}, got)
	require.GreaterOrEqual(t, len(fc.recordedPolls()), 2, "the poll is retried after the draining rejection")
	require.Empty(t, fc.recordedDescribeRuns(), "a draining rejection is retried without describing the workflow")
}

func TestSubscribeSurfacesNonTerminalError(t *testing.T) {
	// A transient failure on a run that is still RUNNING (no rollover, not
	// terminal) must surface rather than retry forever.
	boom := errors.New("boom")
	fc := &fakeSubClient{
		steps: []pollStep{{runID: "R1", getErr: boom}},
		describeByRun: map[string]enumspb.WorkflowExecutionStatus{
			"R1": enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		},
	}
	c := newSubClient(fc)

	var gotErr error
	var yields int
	for _, err := range c.Subscribe(context.Background(), SubscribeOptions{}) {
		yields++
		gotErr = err
	}
	require.Equal(t, 1, yields)
	require.ErrorIs(t, gotErr, boom, "a non-terminal poll error is surfaced to the caller")
}

func TestSubscribeContextCanceledBeforePolling(t *testing.T) {
	fc := &fakeSubClient{steps: []pollStep{
		{result: PollResult{Items: []WireItem{wireItem(t, "evt", "a", 1)}, NextOffset: 2}},
	}}
	c := newSubClient(fc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotErr error
	for _, err := range c.Subscribe(ctx, SubscribeOptions{}) {
		gotErr = err
	}
	require.ErrorIs(t, gotErr, context.Canceled)
	require.Empty(t, fc.recordedPolls(), "a canceled context short-circuits before any poll")
}

func TestSubscribeCooldownCanceledByContext(t *testing.T) {
	// First poll succeeds with no items and MoreReady=false, so the loop enters
	// the cooldown wait. A long cooldown plus a canceled context proves the wait
	// is interruptible rather than blocking for the full PollCooldown.
	fc := &fakeSubClient{steps: []pollStep{
		{result: PollResult{Items: nil, NextOffset: 1, MoreReady: false}},
	}}
	c := newSubClient(fc)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	var gotErr error
	for _, err := range c.Subscribe(ctx, SubscribeOptions{PollCooldown: time.Hour}) {
		gotErr = err
	}
	require.ErrorIs(t, gotErr, context.Canceled)
	require.Less(t, time.Since(start), 5*time.Second, "cooldown wait should be interrupted by context cancellation")
}

func TestSubscribeCooldownAppliedWhenNotMoreReady(t *testing.T) {
	const cooldown = 60 * time.Millisecond
	fc := &fakeSubClient{steps: []pollStep{
		{result: PollResult{Items: nil, NextOffset: 1, MoreReady: false}}, // triggers cooldown
		{result: PollResult{Items: []WireItem{wireItem(t, "evt", "a", 1)}, NextOffset: 2, MoreReady: true}},
	}}
	c := newSubClient(fc)

	start := time.Now()
	for item, err := range c.Subscribe(context.Background(), SubscribeOptions{PollCooldown: cooldown}) {
		require.NoError(t, err)
		require.Equal(t, "a", decodeItem(t, item.Data))
		break
	}
	require.GreaterOrEqual(t, time.Since(start), cooldown/2, "a cooldown is applied between polls when MoreReady is false")
}
