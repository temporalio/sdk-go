package test_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// ---------------------------------------------------------------------------
// memStorageDriver — in-memory StorageDriver for integration tests
// ---------------------------------------------------------------------------

type memStorageDriver struct {
	name          string
	mu            sync.Mutex
	data          map[string]*commonpb.Payload
	storeCount    int
	retrieveCount int
	retrieveErr   error
	storeContexts []converter.StorageDriverStoreContext
}

func newMemDriver(name string) *memStorageDriver {
	return &memStorageDriver{name: name, data: make(map[string]*commonpb.Payload)}
}

func (d *memStorageDriver) Name() string { return d.name }
func (d *memStorageDriver) Type() string { return "mem" }

func (d *memStorageDriver) Store(ctx converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageDriverClaim, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeCount++
	d.storeContexts = append(d.storeContexts, ctx)
	claims := make([]converter.StorageDriverClaim, len(payloads))
	for i, p := range payloads {
		key := uuid.NewString()
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = converter.StorageDriverClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *memStorageDriver) Retrieve(_ converter.StorageDriverRetrieveContext, claims []converter.StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retrieveCount++
	if d.retrieveErr != nil {
		return nil, d.retrieveErr
	}
	payloads := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		p, ok := d.data[c.ClaimData["key"]]
		if !ok {
			return nil, fmt.Errorf("key not found: %q", c.ClaimData["key"])
		}
		payloads[i] = proto.Clone(p).(*commonpb.Payload)
	}
	return payloads, nil
}

func (d *memStorageDriver) getStoreCounts() (store, retrieve int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.storeCount, d.retrieveCount
}

func (d *memStorageDriver) getStoreContexts() []converter.StorageDriverStoreContext {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]converter.StorageDriverStoreContext, len(d.storeContexts))
	copy(out, d.storeContexts)
	return out
}

func (d *memStorageDriver) setRetrieveErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retrieveErr = err
}

func (d *memStorageDriver) resetStoreContexts() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeContexts = nil
}

// ---------------------------------------------------------------------------
// panicMemDriver — wraps memStorageDriver and panics on demand
// ---------------------------------------------------------------------------

type panicMemDriver struct {
	*memStorageDriver
	panicOnStore    bool
	panicOnRetrieve bool
}

func (d *panicMemDriver) Type() string { return "panic" }

func (d *panicMemDriver) Store(ctx converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageDriverClaim, error) {
	claims, err := d.memStorageDriver.Store(ctx, payloads)
	if err != nil {
		return nil, err
	}
	if d.panicOnStore {
		panic("store panic")
	}
	return claims, nil
}

func (d *panicMemDriver) Retrieve(ctx converter.StorageDriverRetrieveContext, claims []converter.StorageDriverClaim) ([]*commonpb.Payload, error) {
	payloads, err := d.memStorageDriver.Retrieve(ctx, claims)
	if err != nil {
		return nil, err
	}
	if d.panicOnRetrieve {
		panic("retrieve panic")
	}
	return payloads, nil
}

// ---------------------------------------------------------------------------
// ExternalStorageTestSuite
// ---------------------------------------------------------------------------

const extStoreThreshold = 128 // bytes — low enough for small test payloads to trigger storage

type ExternalStorageTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	driver *memStorageDriver
	client client.Client
	worker worker.Worker
}

func TestExternalStorageSuite(t *testing.T) {
	suite.Run(t, new(ExternalStorageTestSuite))
}

func (s *ExternalStorageTestSuite) SetupSuite() {
	s.Assertions = require.New(s.T())
	s.NoError(s.InitConfigAndNamespace())
}

func (s *ExternalStorageTestSuite) SetupTest() {
	s.taskQueueName = taskQueuePrefix + "-ext-" + s.T().Name()
	s.driver = newMemDriver("test")
	var err error
	s.client, err = client.Dial(client.Options{
		HostPort:  s.config.ServiceAddr,
		Namespace: s.config.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{s.driver},
			PayloadSizeThreshold: extStoreThreshold,
		},
		ConnectionOptions:       client.ConnectionOptions{TLS: s.config.TLS},
		WorkerHeartbeatInterval: -1,
	})
	s.NoError(err)
	s.worker = worker.New(s.client, s.taskQueueName, worker.Options{
		WorkflowPanicPolicy: worker.FailWorkflow,
	})
}

func (s *ExternalStorageTestSuite) TearDownTest() {
	s.worker.Stop()
	s.client.Close()
}

// oversized returns a string that exceeds extStoreThreshold by extra bytes.
func oversized(extra int) string {
	b := make([]byte, extStoreThreshold+extra)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// startWorkflowOpts returns minimal start options for a test workflow.
func (s *ExternalStorageTestSuite) startOpts(id string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                       id,
		TaskQueue:                s.taskQueueName,
		WorkflowExecutionTimeout: 30 * time.Second,
		WorkflowTaskTimeout:      5 * time.Second,
	}
}

// ---------------------------------------------------------------------------
// TestWorkflowInput — large workflow input is stored on start, retrieved by worker
// ---------------------------------------------------------------------------

func extStoreEchoWorkflow(ctx workflow.Context, input string) (string, error) {
	return input, nil
}

func (s *ExternalStorageTestSuite) TestWorkflowInput() {
	s.worker.RegisterWorkflow(extStoreEchoWorkflow)
	s.NoError(s.worker.Start())

	large := oversized(72)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts("ext-input-"+uuid.NewString()), extStoreEchoWorkflow, large)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(large, result)

	storeCount, retrieveCount := s.driver.getStoreCounts()
	s.Greater(storeCount, 0, "client should have stored the large input")
	s.Greater(retrieveCount, 0, "worker should have retrieved the stored input")
}

// ---------------------------------------------------------------------------
// TestActivityResult — large activity result is stored by worker, retrieved by client
// ---------------------------------------------------------------------------

func extStoreActivityResultWF(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var result string
	err := workflow.ExecuteActivity(ctx, extStoreLargeResultActivity).Get(ctx, &result)
	return result, err
}

func extStoreLargeResultActivity(_ context.Context) (string, error) {
	return oversized(72), nil
}

func (s *ExternalStorageTestSuite) TestActivityResult() {
	s.worker.RegisterWorkflow(extStoreActivityResultWF)
	s.worker.RegisterActivity(extStoreLargeResultActivity)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts("ext-activity-"+uuid.NewString()), extStoreActivityResultWF)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(oversized(72), result)

	storeCount, _ := s.driver.getStoreCounts()
	s.Greater(storeCount, 0, "worker should have stored the large activity result")
}

// ---------------------------------------------------------------------------
// TestSignal — large signal payload is stored by client, retrieved by worker
// ---------------------------------------------------------------------------

var extStoreSignalCh = "ext-store-signal"

func extStoreSignalWorkflow(ctx workflow.Context) (string, error) {
	var received string
	workflow.GetSignalChannel(ctx, extStoreSignalCh).Receive(ctx, &received)
	return received, nil
}

func (s *ExternalStorageTestSuite) TestSignal() {
	s.worker.RegisterWorkflow(extStoreSignalWorkflow)
	s.NoError(s.worker.Start())

	large := oversized(72)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-signal-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreSignalWorkflow)
	s.NoError(err)

	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreSignalCh, large))

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(large, result)

	storeCount, retrieveCount := s.driver.getStoreCounts()
	s.Greater(storeCount, 0, "client should have stored the large signal payload")
	s.Greater(retrieveCount, 0, "worker should have retrieved the stored signal payload")
}

// ---------------------------------------------------------------------------
// TestQuery — large query result stored by worker, retrieved by client
// ---------------------------------------------------------------------------

var extStoreQueryType = "ext-query"
var extStoreQueryDone = "ext-query-done"

func extStoreQueryWorkflow(ctx workflow.Context, queryResult string) error {
	_ = workflow.SetQueryHandler(ctx, extStoreQueryType, func() (string, error) {
		return queryResult, nil
	})
	workflow.GetSignalChannel(ctx, extStoreQueryDone).Receive(ctx, nil)
	return nil
}

func (s *ExternalStorageTestSuite) TestQuery() {
	s.worker.RegisterWorkflow(extStoreQueryWorkflow)
	s.NoError(s.worker.Start())

	large := oversized(72)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-query-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreQueryWorkflow, large)
	s.NoError(err)

	// Poll until the workflow has registered its query handler.
	var queryResp converter.EncodedValue
	s.Eventually(func() bool {
		queryResp, err = s.client.QueryWorkflow(ctx, wfID, run.GetRunID(), extStoreQueryType)
		return err == nil
	}, 10*time.Second, 200*time.Millisecond)

	var result string
	s.NoError(queryResp.Get(&result))
	s.Equal(large, result)

	// Unblock the workflow.
	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreQueryDone, nil))
	s.NoError(run.Get(ctx, nil))

	_, retrieveCount := s.driver.getStoreCounts()
	s.Greater(retrieveCount, 0, "client should have retrieved the large query result")
}

// ---------------------------------------------------------------------------
// TestMixedSizes — only oversized payloads are stored; small ones are inline
// ---------------------------------------------------------------------------

func extStoreMixedWorkflow(_ workflow.Context, small, large string) (string, error) {
	// Return small arg and length of large arg to confirm both were received
	// without producing an oversized result that would trigger an extra store call.
	return fmt.Sprintf("%s|%d", small, len(large)), nil
}

func (s *ExternalStorageTestSuite) TestMixedSizes() {
	s.worker.RegisterWorkflow(extStoreMixedWorkflow)
	s.NoError(s.worker.Start())

	small := "hi"          // well below extStoreThreshold
	large := oversized(72) // above extStoreThreshold

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts("ext-mixed-"+uuid.NewString()), extStoreMixedWorkflow, small, large)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(fmt.Sprintf("%s|%d", small, len(large)), result)

	storeCount, _ := s.driver.getStoreCounts()
	// Only the large arg should have been stored (the small arg is inline).
	s.Equal(1, storeCount, "only the oversized arg should be stored")
}

// ---------------------------------------------------------------------------
// TestDriverSelector — selector routes payloads to two drivers by size
// ---------------------------------------------------------------------------

func extStoreSelectorWorkflow(_ workflow.Context, a, b string) (string, error) {
	// Return only the lengths to confirm both args were received without
	// producing an oversized result that would trigger an extra store call.
	return fmt.Sprintf("%d:%d", len(a), len(b)), nil
}

func (s *ExternalStorageTestSuite) TestDriverSelector() {
	d1 := newMemDriver("d1")
	d2 := newMemDriver("d2")

	var selectIdx int
	var selectMu sync.Mutex
	selector := &roundRobinSelector{drivers: []converter.StorageDriver{d1, d2}, mu: &selectMu, idx: &selectIdx}

	var err error
	s.client.Close()
	s.client, err = client.Dial(client.Options{
		HostPort:  s.config.ServiceAddr,
		Namespace: s.config.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{d1, d2},
			DriverSelector:       selector,
			PayloadSizeThreshold: extStoreThreshold,
		},
		ConnectionOptions:       client.ConnectionOptions{TLS: s.config.TLS},
		WorkerHeartbeatInterval: -1,
	})
	s.NoError(err)
	// Re-create worker bound to the new client.
	s.worker.Stop()
	s.worker = worker.New(s.client, s.taskQueueName, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	s.worker.RegisterWorkflow(extStoreSelectorWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	a := oversized(72)
	b := oversized(72)
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts("ext-selector-"+uuid.NewString()), extStoreSelectorWorkflow, a, b)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(fmt.Sprintf("%d:%d", len(a), len(b)), result)

	d1Store, _ := d1.getStoreCounts()
	d2Store, _ := d2.getStoreCounts()
	s.Equal(2, d1Store+d2Store, "both oversized args should be stored across the two drivers")
	s.Greater(d1Store, 0, "d1 should have been used")
	s.Greater(d2Store, 0, "d2 should have been used")
}

type roundRobinSelector struct {
	drivers []converter.StorageDriver
	mu      *sync.Mutex
	idx     *int
}

func (r *roundRobinSelector) SelectDriver(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.drivers[*r.idx%len(r.drivers)]
	*r.idx++
	return d, nil
}

// ---------------------------------------------------------------------------
// TestRetrieveFailure — driver fails on retrieve; workflow task repeatedly
// fails until execution timeout is reached.
// ---------------------------------------------------------------------------

func extStoreRetrieveFailWorkflow(ctx workflow.Context, _ string) (string, error) {
	return "should not reach here", nil
}

func (s *ExternalStorageTestSuite) TestRetrieveFailure() {
	// Configure the driver to fail on every Retrieve call.
	s.driver.setRetrieveErr(fmt.Errorf("storage unavailable"))

	s.worker.RegisterWorkflow(extStoreRetrieveFailWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	opts := s.startOpts("ext-retrieve-fail-" + uuid.NewString())
	opts.WorkflowExecutionTimeout = 5 * time.Second // fail fast
	opts.WorkflowTaskTimeout = 2 * time.Second

	run, err := s.client.ExecuteWorkflow(ctx, opts, extStoreRetrieveFailWorkflow, oversized(72))
	s.NoError(err)

	err = run.Get(ctx, nil)
	s.Error(err, "workflow should fail because retrieve always errors")

	_, retrieveCount := s.driver.getStoreCounts()
	s.Greater(retrieveCount, 0, "worker should have attempted at least one retrieve")

	storeCount, _ := s.driver.getStoreCounts()
	s.Greater(storeCount, 0, "client should have stored the input")
}

// ---------------------------------------------------------------------------
// TestNoStorageWhenBelowThreshold — sanity check that the driver is never
// called when all payloads are below the threshold.
// ---------------------------------------------------------------------------

func extStoreSmallPayloadWorkflow(ctx workflow.Context, input string) (string, error) {
	return input, nil
}

func (s *ExternalStorageTestSuite) TestNoStorageWhenBelowThreshold() {
	s.worker.RegisterWorkflow(extStoreSmallPayloadWorkflow)
	s.NoError(s.worker.Start())

	small := "tiny" // well below extStoreThreshold
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts("ext-small-"+uuid.NewString()), extStoreSmallPayloadWorkflow, small)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(small, result)

	storeCount, retrieveCount := s.driver.getStoreCounts()
	s.Equal(0, storeCount, "small payloads should never be stored")
	s.Equal(0, retrieveCount, "small payloads should never be retrieved")
}

// ---------------------------------------------------------------------------
// TestDriverPanicOnRetrieve — driver panics on retrieve; expect WFT failure
// is submitted explicitly and workflow eventually fails.
// ---------------------------------------------------------------------------

func (s *ExternalStorageTestSuite) TestDriverPanicOnRetrieve() {
	pd := &panicMemDriver{memStorageDriver: newMemDriver("test"), panicOnRetrieve: true}

	c, err := client.Dial(client.Options{
		HostPort:  s.config.ServiceAddr,
		Namespace: s.config.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{pd},
			PayloadSizeThreshold: extStoreThreshold,
		},
		ConnectionOptions:       client.ConnectionOptions{TLS: s.config.TLS},
		WorkerHeartbeatInterval: -1,
	})
	s.NoError(err)
	defer c.Close()

	w := worker.New(c, s.taskQueueName, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterWorkflow(extStoreEchoWorkflow)
	s.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	opts := s.startOpts("ext-panic-retrieve-" + uuid.NewString())
	opts.WorkflowExecutionTimeout = 5 * time.Second
	opts.WorkflowTaskTimeout = 2 * time.Second

	run, err := c.ExecuteWorkflow(ctx, opts, extStoreEchoWorkflow, oversized(72))
	s.NoError(err)

	err = run.Get(ctx, nil)
	s.Error(err, "workflow should fail because retrieve panics")

	storeCount, retrieveCount := pd.getStoreCounts()
	s.Greater(storeCount, 0, "client should have stored the large input")
	s.Greater(retrieveCount, 0, "worker should have attempted at least one retrieve")
}

// ---------------------------------------------------------------------------
// TestDriverPanicOnStore — driver panics on store (outbound path); expect
// WFT failure is submitted and workflow eventually fails.
// ---------------------------------------------------------------------------

func extStorePanicOnStoreWorkflow(_ workflow.Context) (string, error) {
	return oversized(72), nil
}

func (s *ExternalStorageTestSuite) TestDriverPanicOnStore() {
	pd := &panicMemDriver{memStorageDriver: newMemDriver("test"), panicOnStore: true}

	c, err := client.Dial(client.Options{
		HostPort:  s.config.ServiceAddr,
		Namespace: s.config.Namespace,
		Logger:    ilog.NewDefaultLogger(),
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{pd},
			PayloadSizeThreshold: extStoreThreshold,
		},
		ConnectionOptions:       client.ConnectionOptions{TLS: s.config.TLS},
		WorkerHeartbeatInterval: -1,
	})
	s.NoError(err)
	defer c.Close()

	w := worker.New(c, s.taskQueueName, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterWorkflow(extStorePanicOnStoreWorkflow)
	s.NoError(w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	opts := s.startOpts("ext-panic-store-" + uuid.NewString())
	opts.WorkflowExecutionTimeout = 5 * time.Second
	opts.WorkflowTaskTimeout = 2 * time.Second

	run, err := c.ExecuteWorkflow(ctx, opts, extStorePanicOnStoreWorkflow)
	s.NoError(err)

	err = run.Get(ctx, nil)
	s.Error(err, "workflow should fail because store panics on outbound path")

	storeCount, _ := pd.getStoreCounts()
	s.Greater(storeCount, 0, "worker should have attempted at least one store")
}

// ---------------------------------------------------------------------------
// TestReplayWithExternalStorage — run extStoreEchoWorkflow to completion,
// then replay its history using a WorkflowReplayer configured with the same
// storage driver. Verifies that externally stored payloads are resolved
// before being handed to the workflow code during replay.
// ---------------------------------------------------------------------------

func (s *ExternalStorageTestSuite) TestReplayWithExternalStorage() {
	s.worker.RegisterWorkflow(extStoreEchoWorkflow)
	s.NoError(s.worker.Start())

	large := oversized(72)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-replay-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreEchoWorkflow, large)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(large, result)

	// Replay using the same driver instance so stored payloads can be resolved.
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
		ExternalStorage: converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{s.driver},
			PayloadSizeThreshold: extStoreThreshold,
		},
	})
	s.NoError(err)
	replayer.RegisterWorkflow(extStoreEchoWorkflow)

	s.NoError(replayer.ReplayWorkflowExecution(
		ctx,
		s.client.WorkflowService(),
		nil, // use default logger
		s.config.Namespace,
		workflow.Execution{ID: wfID, RunID: run.GetRunID()},
	))
}

// ---------------------------------------------------------------------------
// TestReplayWithoutExternalStorageFails — history containing external storage
// references cannot be replayed when no storage driver is configured.
// ---------------------------------------------------------------------------

func (s *ExternalStorageTestSuite) TestReplayWithoutExternalStorageFails() {
	s.worker.RegisterWorkflow(extStoreEchoWorkflow)
	s.NoError(s.worker.Start())

	large := oversized(72)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-replay-no-storage-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreEchoWorkflow, large)
	s.NoError(err)

	var result string
	s.NoError(run.Get(ctx, &result))
	s.Equal(large, result)

	// Replayer with no ExternalStorage configured cannot resolve storage references.
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{})
	s.NoError(err)
	replayer.RegisterWorkflow(extStoreEchoWorkflow)

	s.Error(replayer.ReplayWorkflowExecution(
		ctx,
		s.client.WorkflowService(),
		nil,
		s.config.Namespace,
		workflow.Execution{ID: wfID, RunID: run.GetRunID()},
	), "replay should fail when storage references cannot be resolved")
}

// ---------------------------------------------------------------------------
// Target context tests — verify StorageDriverStoreContext.Target is populated
// correctly for every outbound payload visitor call site.
// ---------------------------------------------------------------------------

// extStoreTargetWorkflow is a simple workflow that returns its input unchanged.
func extStoreTargetWorkflow(ctx workflow.Context, input string) (string, error) {
	return input, nil
}

// extStoreTargetSignalWorkflow blocks on a signal and returns the received value.
var extStoreTargetSignalCh = "ext-target-signal"

func extStoreTargetSignalWorkflow(ctx workflow.Context) (string, error) {
	var received string
	workflow.GetSignalChannel(ctx, extStoreTargetSignalCh).Receive(ctx, &received)
	return received, nil
}

// extStoreTargetUpdateWorkflow accepts a single update named "set" that stores
// the provided string, then blocks on a signal before returning it.
var extStoreTargetUpdateName = "set"
var extStoreTargetUpdateDone = "ext-target-update-done"

func extStoreTargetUpdateWorkflow(ctx workflow.Context) (string, error) {
	var value string
	if err := workflow.SetUpdateHandler(ctx, extStoreTargetUpdateName, func(_ workflow.Context, v string) error {
		value = v
		return nil
	}); err != nil {
		return "", err
	}
	workflow.GetSignalChannel(ctx, extStoreTargetUpdateDone).Receive(ctx, nil)
	return value, nil
}

// extStoreTargetActivityWorkflow runs an activity and returns its result.
func extStoreTargetActivityWorkflow(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	var result string
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, ao), extStoreTargetActivity).Get(ctx, &result)
	return result, err
}

func extStoreTargetActivity(_ context.Context) (string, error) {
	return oversized(72), nil
}

func (s *ExternalStorageTestSuite) TestTargetContext_ExecuteWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-exec-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetWorkflow, oversized(72))
	s.NoError(err)
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	// [0] client-side: Start workflow arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreTargetWorkflow",
	}, ctxs[0].Target)

	// [1] WFT #1: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreTargetWorkflow",
		RunID:        run.GetRunID(),
	}, ctxs[1].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_SignalWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetSignalWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-signal-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetSignalWorkflow)
	s.NoError(err)

	// Isolate: only capture stores triggered by SignalWorkflow.
	s.driver.resetStoreContexts()
	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetSignalCh, oversized(72)))

	var result string
	s.NoError(run.Get(ctx, &result))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	// [0] client-side: signal workflow arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:  s.config.Namespace,
		WorkflowID: wfID,
		RunID:      run.GetRunID(),
	}, ctxs[0].Target)

	// [1] WFT #1: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreTargetSignalWorkflow",
	}, ctxs[1].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_UpdateWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetUpdateWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-update-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetUpdateWorkflow)
	s.NoError(err)

	// Isolate: only capture stores triggered by UpdateWorkflow and beyond.
	s.driver.resetStoreContexts()
	handle, err := s.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		UpdateName:   extStoreTargetUpdateName,
		Args:         []interface{}{oversized(72)},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	s.NoError(err)
	s.NoError(handle.Get(ctx, nil))

	// Unblock the workflow.
	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetUpdateDone, nil))
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 3)

	// [0] client-side: workflow update
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:  s.config.Namespace,
		WorkflowID: wfID,
		RunID:      run.GetRunID(),
	}, ctxs[0].Target)

	workerTarget := converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreTargetUpdateWorkflow",
	}

	// [1] WFT #1: Update acceptance arg
	s.Equal(workerTarget, ctxs[1].Target)

	// [2] WFT #2: CompleteWorkflowExecution result
	s.Equal(workerTarget, ctxs[2].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_TerminateWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetSignalWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-terminate-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetSignalWorkflow)
	s.NoError(err)

	// Isolate: only capture stores triggered by TerminateWorkflow.
	s.driver.resetStoreContexts()
	s.NoError(s.client.TerminateWorkflow(ctx, wfID, run.GetRunID(), "test termination", oversized(72)))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: terminate workflow details
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:  s.config.Namespace,
		WorkflowID: wfID,
		RunID:      run.GetRunID(),
	}, ctxs[0].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_ActivityResult() {
	s.worker.RegisterWorkflow(extStoreTargetActivityWorkflow)
	s.worker.RegisterActivity(extStoreTargetActivity)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-activity-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetActivityWorkflow)
	s.NoError(err)
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	expected := converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreTargetActivityWorkflow",
	}

	// [0] WFT #1: CompleteActivity command result
	s.Equal(expected, ctxs[0].Target)

	// [1] WFT #2: CompleteWorkflowExecution command result
	s.Equal(expected, ctxs[1].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_CreateSchedule() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	workflowID := "ext-target-schedule-wf-" + uuid.NewString()
	scheduleID := "ext-target-schedule-" + uuid.NewString()

	handle, err := s.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   scheduleID,
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			ID:                  workflowID,
			Workflow:            extStoreTargetWorkflow,
			Args:                []interface{}{oversized(72)},
			TaskQueue:           s.taskQueueName,
			WorkflowTaskTimeout: 5 * time.Second,
		},
	})
	s.NoError(err)
	defer func() { _ = handle.Delete(ctx) }()

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: create schedule action arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   workflowID,
		WorkflowType: "extStoreTargetWorkflow",
	}, ctxs[0].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_UpdateSchedule() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	workflowID := "ext-target-schedule-wf-" + uuid.NewString()
	scheduleID := "ext-target-schedule-" + uuid.NewString()

	handle, err := s.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   scheduleID,
		Spec: client.ScheduleSpec{},
		Action: &client.ScheduleWorkflowAction{
			ID:                  workflowID,
			Workflow:            extStoreTargetWorkflow,
			Args:                []interface{}{"small"}, // below threshold; satisfies arg validation
			TaskQueue:           s.taskQueueName,
			WorkflowTaskTimeout: 5 * time.Second,
		},
	})
	s.NoError(err)
	defer func() { _ = handle.Delete(ctx) }()

	// Isolate: only capture stores triggered by Update.
	s.driver.resetStoreContexts()

	s.NoError(handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			action := input.Description.Schedule.Action.(*client.ScheduleWorkflowAction)
			action.Args = []interface{}{oversized(72)}
			return &client.ScheduleUpdate{Schedule: &input.Description.Schedule}, nil
		},
	}))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: update schedule action arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   workflowID,
		WorkflowType: "extStoreTargetWorkflow",
	}, ctxs[0].Target)
}

func extStoreTargetStandaloneActivity(_ context.Context, _ string) (string, error) {
	return oversized(72), nil
}

func (s *ExternalStorageTestSuite) TestTargetContext_ExecuteStandaloneActivity() {
	if os.Getenv("DISABLE_STANDALONE_ACTIVITY_TESTS") != "" {
		s.T().SkipNow()
	}

	s.worker.RegisterActivity(extStoreTargetStandaloneActivity)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	activityID := "ext-target-standalone-" + uuid.NewString()
	handle, err := s.client.ExecuteActivity(ctx, client.StartActivityOptions{
		ID:                  activityID,
		TaskQueue:           s.taskQueueName,
		StartToCloseTimeout: 10 * time.Second,
	}, extStoreTargetStandaloneActivity, oversized(72))
	s.NoError(err)
	s.NoError(handle.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	// [0] client-side: start activity arg
	s.Equal(converter.StorageDriverActivityInfo{
		Namespace:    s.config.Namespace,
		ActivityID:   activityID,
		ActivityType: "extStoreTargetStandaloneActivity",
	}, ctxs[0].Target)

	// [1] WFT #1: CompleteActivity command result
	s.Equal(converter.StorageDriverActivityInfo{
		Namespace:    s.config.Namespace,
		ActivityID:   activityID,
		RunID:        handle.GetRunID(),
		ActivityType: "extStoreTargetStandaloneActivity",
	}, ctxs[1].Target)
}

func extStoreAsyncActivityWorkflow(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		ActivityID:          "async-act",
		StartToCloseTimeout: 15 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	var result string
	_ = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, ao), "extStoreAsyncActivity").Get(ctx, &result)
	return nil
}

var extStoreTargetQueryName = "ext-target-query"
var extStoreTargetQueryDoneCh = "ext-target-query-done"

func extStoreTargetQueryWorkflow(ctx workflow.Context) error {
	_ = workflow.SetQueryHandler(ctx, extStoreTargetQueryName, func(_ string) (string, error) {
		return "ok", nil
	})
	workflow.GetSignalChannel(ctx, extStoreTargetQueryDoneCh).Receive(ctx, nil)
	return nil
}

func (s *ExternalStorageTestSuite) TestTargetContext_QueryWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetQueryWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-query-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreTargetQueryWorkflow)
	s.NoError(err)

	// Wait for the query handler to be registered using a small arg.
	s.Eventually(func() bool {
		_, err = s.client.QueryWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetQueryName, "ping")
		return err == nil
	}, 10*time.Second, 200*time.Millisecond)

	// Isolate: only capture stores triggered by QueryWorkflow with an oversized arg.
	s.driver.resetStoreContexts()

	_, err = s.client.QueryWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetQueryName, oversized(72))
	s.NoError(err)

	// Unblock the workflow.
	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetQueryDoneCh, nil))
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: query arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:  s.config.Namespace,
		WorkflowID: wfID,
		RunID:      run.GetRunID(),
	}, ctxs[0].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_SignalWithStartWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetSignalWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-signal-start-" + uuid.NewString()
	run, err := s.client.SignalWithStartWorkflow(ctx, wfID, extStoreTargetSignalCh, oversized(72),
		s.startOpts(wfID), extStoreTargetSignalWorkflow)
	s.NoError(err)
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	// [0] client-side: signal-with-start signal arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreTargetSignalWorkflow",
	}, ctxs[0].Target)

	// [1] WFT #1: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreTargetSignalWorkflow",
	}, ctxs[1].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_UpdateWithStartWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetUpdateWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-update-start-" + uuid.NewString()
	opts := s.startOpts(wfID)
	opts.WorkflowIDConflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL

	startOp := s.client.NewWithStartWorkflowOperation(opts, extStoreTargetUpdateWorkflow)
	handle, err := s.client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName:   extStoreTargetUpdateName,
			Args:         []interface{}{oversized(72)},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	s.NoError(err)
	s.NoError(handle.Get(ctx, nil))

	run, err := startOp.Get(ctx)
	s.NoError(err)

	// Unblock the workflow.
	s.NoError(s.client.SignalWorkflow(ctx, wfID, run.GetRunID(), extStoreTargetUpdateDone, nil))
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 3)

	// [0] client-side: update-with-start update arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreTargetUpdateWorkflow",
	}, ctxs[0].Target)

	workerTarget := converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreTargetUpdateWorkflow",
	}

	// [1] WFT #1: update acceptance arg
	s.Equal(workerTarget, ctxs[1].Target)

	// [2] WFT #2: CompleteWorkflowExecution result
	s.Equal(workerTarget, ctxs[2].Target)
}
func (s *ExternalStorageTestSuite) TestTargetContext_CompleteActivity() {
	taskTokenCh := make(chan []byte, 1)
	s.worker.RegisterActivityWithOptions(func(ctx context.Context) error {
		taskTokenCh <- activity.GetInfo(ctx).TaskToken
		return activity.ErrResultPending
	}, activity.RegisterOptions{Name: "extStoreAsyncActivity"})
	s.worker.RegisterWorkflow(extStoreAsyncActivityWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-complete-act-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreAsyncActivityWorkflow)
	s.NoError(err)

	var taskToken []byte
	select {
	case taskToken = <-taskTokenCh:
	case <-ctx.Done():
		s.Fail("timed out waiting for activity task token")
	}

	// Isolate: only capture stores triggered by CompleteActivity.
	s.driver.resetStoreContexts()
	s.NoError(s.client.CompleteActivityWithOptions(ctx, client.CompleteActivityOptions{
		TaskToken:    taskToken,
		WorkflowID:   wfID,
		WorkflowType: "extStoreAsyncActivityWorkflow",
		Result:       oversized(72),
	}))
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: complete activity input result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreAsyncActivityWorkflow",
	}, ctxs[0].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_CompleteActivityByID() {
	taskTokenCh := make(chan []byte, 1)
	s.worker.RegisterActivityWithOptions(func(ctx context.Context) error {
		taskTokenCh <- activity.GetInfo(ctx).TaskToken
		return activity.ErrResultPending
	}, activity.RegisterOptions{Name: "extStoreAsyncActivity"})
	s.worker.RegisterWorkflow(extStoreAsyncActivityWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-complete-byid-" + uuid.NewString()
	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreAsyncActivityWorkflow)
	s.NoError(err)

	select {
	case <-taskTokenCh:
	case <-ctx.Done():
		s.Fail("timed out waiting for activity to start")
	}

	// Isolate: only capture stores triggered by CompleteActivityByID.
	s.driver.resetStoreContexts()
	s.NoError(s.client.CompleteActivityByIDWithOptions(ctx, client.CompleteActivityByIDOptions{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		ActivityID:   "async-act",
		WorkflowType: "extStoreAsyncActivityWorkflow",
		Result:       oversized(72),
	}))
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: complete activity input result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        run.GetRunID(),
		WorkflowType: "extStoreAsyncActivityWorkflow",
	}, ctxs[0].Target)
}

func (s *ExternalStorageTestSuite) TestTargetContext_CompleteActivityByActivityID() {
	if os.Getenv("DISABLE_STANDALONE_ACTIVITY_TESTS") != "" {
		s.T().SkipNow()
	}

	taskTokenCh := make(chan []byte, 1)
	s.worker.RegisterActivityWithOptions(func(ctx context.Context) error {
		taskTokenCh <- activity.GetInfo(ctx).TaskToken
		return activity.ErrResultPending
	}, activity.RegisterOptions{Name: "extStoreAsyncStandaloneActivity"})
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	activityID := "ext-target-complete-standalone-" + uuid.NewString()
	handle, err := s.client.ExecuteActivity(ctx, client.StartActivityOptions{
		ID:                  activityID,
		TaskQueue:           s.taskQueueName,
		StartToCloseTimeout: 15 * time.Second,
	}, "extStoreAsyncStandaloneActivity")
	s.NoError(err)

	select {
	case <-taskTokenCh:
	case <-ctx.Done():
		s.Fail("timed out waiting for standalone activity to start")
	}

	// Isolate: only capture stores triggered by CompleteActivityByActivityID.
	s.driver.resetStoreContexts()
	s.NoError(s.client.CompleteActivityByActivityIDWithOptions(ctx, client.CompleteActivityByActivityIDOptions{
		Namespace:     s.config.Namespace,
		ActivityID:    activityID,
		ActivityRunID: handle.GetRunID(),
		ActivityType:  "extStoreAsyncStandaloneActivity",
		Result:        oversized(72),
	}))
	s.NoError(handle.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 1)

	// [0] client-side: complete activity input result
	s.Equal(converter.StorageDriverActivityInfo{
		Namespace:    s.config.Namespace,
		ActivityID:   activityID,
		RunID:        handle.GetRunID(),
		ActivityType: "extStoreAsyncStandaloneActivity",
	}, ctxs[0].Target)
}

// extStoreCaNWorkflow continues-as-new on its first run (empty input), passing
// an oversized payload to the next run, which simply returns that value.
func extStoreCaNWorkflow(ctx workflow.Context, input string) (string, error) {
	if input == "" {
		return "", workflow.NewContinueAsNewError(ctx, extStoreCaNWorkflow, oversized(72))
	}
	return input, nil
}

func (s *ExternalStorageTestSuite) TestTargetContext_ContinueAsNew() {
	s.worker.RegisterWorkflow(extStoreCaNWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfID := "ext-target-can-" + uuid.NewString()
	firstRun, err := s.client.ExecuteWorkflow(ctx, s.startOpts(wfID), extStoreCaNWorkflow, "")
	s.NoError(err)
	firstRunID := firstRun.GetRunID() // capture before Get() follows the CAN chain
	s.NoError(firstRun.Get(ctx, nil))
	lastRunID := firstRun.GetRunID()

	s.NotEqual(firstRunID, lastRunID)

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 2)

	// [0] First run WFT #1: ContinueAsNewWorkflowExecution arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		WorkflowType: "extStoreCaNWorkflow",
	}, ctxs[0].Target)

	// [1] Second run WFT #1: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   wfID,
		RunID:        lastRunID,
		WorkflowType: "extStoreCaNWorkflow",
	}, ctxs[1].Target)
}

// extStoreCaNChildWorkflow always continues-as-new on its first run (regardless
// of input), then returns the input unchanged on the second run.
func extStoreCaNChildWorkflow(ctx workflow.Context, input string) (string, error) {
	if workflow.GetInfo(ctx).ContinuedExecutionRunID == "" {
		return "", workflow.NewContinueAsNewError(ctx, extStoreCaNChildWorkflow, input)
	}
	return input, nil
}

// extStoreCaNParentWorkflow starts extStoreCaNChildWorkflow as a child and
// returns its result.
func extStoreCaNParentWorkflow(ctx workflow.Context) (string, error) {
	info := workflow.GetInfo(ctx)
	childOpts := workflow.ChildWorkflowOptions{WorkflowID: info.WorkflowExecution.ID + "/child"}
	var result string
	err := workflow.ExecuteChildWorkflow(
		workflow.WithChildOptions(ctx, childOpts),
		extStoreCaNChildWorkflow,
		oversized(72),
	).Get(ctx, &result)
	return result, err
}

func (s *ExternalStorageTestSuite) TestTargetContext_ChildWorkflowContinuedAsNew() {
	s.worker.RegisterWorkflow(extStoreCaNParentWorkflow)
	s.worker.RegisterWorkflow(extStoreCaNChildWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	parentWFID := "ext-target-can-child-" + uuid.NewString()
	childWFID := parentWFID + "/child"

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(parentWFID), extStoreCaNParentWorkflow)
	s.NoError(err)
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 4)

	childTarget := converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   childWFID,
		WorkflowType: "extStoreCaNChildWorkflow",
	}

	// [0] Parent WFT #1: StartChildWorkflowExecution arg
	s.Equal(childTarget, ctxs[0].Target)

	// [1] Child run 1 WFT #1: ContinueAsNewWorkflowExecution input
	s.Equal(childTarget, ctxs[1].Target)

	// [2] Child run 2 WFT #1: CompleteWorkflowExecution result
	childRun2Info, ok := ctxs[2].Target.(converter.StorageDriverWorkflowInfo)
	s.Require().True(ok)
	s.Equal(s.config.Namespace, childRun2Info.Namespace)
	s.Equal(childWFID, childRun2Info.WorkflowID)
	s.Equal("extStoreCaNChildWorkflow", childRun2Info.WorkflowType)
	s.NotEmpty(childRun2Info.RunID)

	// [3] Parent WFT #2: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   parentWFID,
		WorkflowType: "extStoreCaNParentWorkflow",
		RunID:        run.GetRunID(),
	}, ctxs[3].Target)
}

// extStoreTargetParentWorkflow starts a child workflow with a deterministic
// ID (parentID+"/child") passing its input through, then returns the child's result.
func extStoreTargetParentWorkflow(ctx workflow.Context, input string) (string, error) {
	info := workflow.GetInfo(ctx)
	childOpts := workflow.ChildWorkflowOptions{WorkflowID: info.WorkflowExecution.ID + "/child"}
	var result string
	err := workflow.ExecuteChildWorkflow(
		workflow.WithChildOptions(ctx, childOpts),
		extStoreTargetWorkflow,
		input,
	).Get(ctx, &result)
	return result, err
}

func (s *ExternalStorageTestSuite) TestTargetContext_StartChildWorkflow() {
	s.worker.RegisterWorkflow(extStoreTargetParentWorkflow)
	s.worker.RegisterWorkflow(extStoreTargetWorkflow)
	s.NoError(s.worker.Start())

	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	parentWFID := "ext-target-child-" + uuid.NewString()
	childWFID := parentWFID + "/child"

	run, err := s.client.ExecuteWorkflow(ctx, s.startOpts(parentWFID), extStoreTargetParentWorkflow, oversized(72))
	s.NoError(err)
	s.NoError(run.Get(ctx, nil))

	ctxs := s.driver.getStoreContexts()
	s.Len(ctxs, 4)

	// [0] client-side: start workflow arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   parentWFID,
		WorkflowType: "extStoreTargetParentWorkflow",
	}, ctxs[0].Target)

	// [1] parent WFT #1: StartChildWorkflowExecution arg
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   childWFID,
		WorkflowType: "extStoreTargetWorkflow",
	}, ctxs[1].Target)

	// [2] child WFT #1: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:  s.config.Namespace,
		WorkflowID: parentWFID,
		RunID:      run.GetRunID(),
	}, ctxs[2].Target)

	// [3] parent WFT #2: CompleteWorkflowExecution result
	s.Equal(converter.StorageDriverWorkflowInfo{
		Namespace:    s.config.Namespace,
		WorkflowID:   parentWFID,
		WorkflowType: "extStoreTargetParentWorkflow",
		RunID:        run.GetRunID(),
	}, ctxs[3].Target)
}
