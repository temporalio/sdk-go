package workflowstreams

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// fakeClient implements just the client.Client methods Client uses. Embedding
// the interface satisfies the rest; calling an unimplemented method panics,
// which is fine because the tests never exercise them.
type fakeClient struct {
	client.Client

	mu        sync.Mutex
	signals   []PublishInput
	signalErr error // when set, SignalWorkflow returns this error
}

func (f *fakeClient) SignalWorkflow(_ context.Context, _, _, signalName string, arg any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.signalErr != nil {
		return f.signalErr
	}
	if signalName == PublishSignalName {
		f.signals = append(f.signals, arg.(PublishInput))
	}
	return nil
}

func (f *fakeClient) recorded() []PublishInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]PublishInput(nil), f.signals...)
}

func decodeFirstItem(t *testing.T, in PublishInput, idx int) string {
	t.Helper()
	payload, err := decodePayloadWire(in.Items[idx].Data)
	require.NoError(t, err)
	var s string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(payload, &s))
	return s
}

func TestFlushSendsBufferedItems(t *testing.T) {
	fc := &fakeClient{}
	c := NewClient(fc, "wf-1", Options{})
	c.Topic("events").Publish("a", false)
	c.Topic("events").Publish("b", false)

	require.NoError(t, c.Flush(context.Background()))

	sigs := fc.recorded()
	require.Len(t, sigs, 1)
	require.Len(t, sigs[0].Items, 2)
	require.EqualValues(t, 1, sigs[0].Sequence)
	require.NotEmpty(t, sigs[0].PublisherID)
	require.Equal(t, "a", decodeFirstItem(t, sigs[0], 0))
	require.Equal(t, "b", decodeFirstItem(t, sigs[0], 1))

	require.NoError(t, c.Close(context.Background()))
}

func TestFlushNoopWhenEmpty(t *testing.T) {
	fc := &fakeClient{}
	c := NewClient(fc, "wf-1", Options{})
	require.NoError(t, c.Flush(context.Background()))
	require.Empty(t, fc.recorded())
}

func TestSequenceAdvancesAcrossFlushes(t *testing.T) {
	fc := &fakeClient{}
	c := NewClient(fc, "wf-1", Options{})

	c.Topic("t").Publish("x", false)
	require.NoError(t, c.Flush(context.Background()))
	c.Topic("t").Publish("y", false)
	require.NoError(t, c.Flush(context.Background()))

	sigs := fc.recorded()
	require.Len(t, sigs, 2)
	require.EqualValues(t, 1, sigs[0].Sequence)
	require.EqualValues(t, 2, sigs[1].Sequence)
	require.Equal(t, sigs[0].PublisherID, sigs[1].PublisherID)

	require.NoError(t, c.Close(context.Background()))
}

func TestMaxBatchSizeTriggersFlush(t *testing.T) {
	fc := &fakeClient{}
	// Long interval so only the size threshold can trigger a flush.
	c := NewClient(fc, "wf-1", Options{BatchInterval: time.Hour, MaxBatchSize: 2})

	c.Topic("t").Publish("a", false)
	c.Topic("t").Publish("b", false) // reaches MaxBatchSize → flush

	require.Eventually(t, func() bool {
		return len(fc.recorded()) == 1
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, c.Close(context.Background()))
}

func TestCloseDrainsBuffer(t *testing.T) {
	fc := &fakeClient{}
	c := NewClient(fc, "wf-1", Options{BatchInterval: time.Hour})

	c.Topic("t").Publish("a", false)
	require.NoError(t, c.Close(context.Background()))

	sigs := fc.recorded()
	require.Len(t, sigs, 1)
	require.Len(t, sigs[0].Items, 1)
}

func TestForceFlush(t *testing.T) {
	fc := &fakeClient{}
	c := NewClient(fc, "wf-1", Options{BatchInterval: time.Hour})

	c.Topic("t").Publish("a", true) // forceFlush

	require.Eventually(t, func() bool {
		return len(fc.recorded()) == 1
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, c.Close(context.Background()))
}

func TestFlushTimeoutAfterMaxRetryDuration(t *testing.T) {
	const retryWindow = time.Millisecond
	fc := &fakeClient{signalErr: errors.New("boom")}
	c := NewClient(fc, "wf-1", Options{BatchInterval: time.Hour, MaxRetryDuration: retryWindow})

	c.Topic("t").Publish("a", false)

	// The first flush sets pending and fails to send (transient "boom").
	require.Error(t, c.Flush(context.Background()))

	// Wait past the retry window with ample margin for coarse OS timer
	// granularity (notably on Windows, where sub-tick durations can read as
	// zero). The next flush sees the window exceeded and returns
	// FlushTimeoutError.
	time.Sleep(50 * time.Millisecond)

	var fte *FlushTimeoutError
	require.ErrorAs(t, c.Flush(context.Background()), &fte)

	require.NoError(t, c.Close(context.Background()))
}
