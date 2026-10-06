// Startup tests use these types to build real workers with typed service mocks.
// Logger, metric, and plugin callbacks report selected events without holding the
// recording mutex. Channel gates hold those callbacks until each test releases
// them, so tests observe Start, Run, and Stop without adding SDK pause hooks.
package internal

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	genenumspb "go.temporal.io/api/enums/v1"
	gennamespacepb "go.temporal.io/api/namespace/v1"
	gentaskqueuepb "go.temporal.io/api/taskqueue/v1"
	genworkflowservice "go.temporal.io/api/workflowservice/v1"
	genworkflowservicemock "go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"

	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

type (
	startupFixture struct {
		worker        *AggregatedWorker
		client        *WorkflowClient
		hooks         *startupHooks
		tuner         *startupTuner
		pollStarted   chan struct{}
		pollOnce      sync.Once
		shutdown      *startupGate
		shutdownCalls atomic.Int32
		describe      func()
		heartbeats    bool
		groups        *gentaskqueuepb.PollerGroupsInfo
		autoStop      bool
	}

	// After construction, hooks record selected startup events and invoke the
	// test's synchronous callback without holding the recording mutex.
	startupHooks struct {
		armed          atomic.Bool
		nexusPreparing atomic.Bool
		mu             sync.Mutex
		events         []string
		activity       int
		activityLogs   int
		onEvent        func(string)
		onFatal        func(error)
		skipOnly       bool
	}

	startupGate struct {
		entered chan struct{}
		release chan struct{}
		enter   sync.Once
		open    func()
	}

	startupLogger struct {
		hooks  *startupHooks
		fields []any
	}

	// log.With uses WithCallerSkip only when the supplied logger has no With
	// method. This separate type exercises that existing alternative.
	startupSkipLogger struct {
		hooks *startupHooks
	}

	startupMetrics struct {
		hooks *startupHooks
		tags  map[string]string
	}

	startupTuner struct {
		WorkerTuner
		hooks    *startupHooks
		workflow SlotSupplier
		nexus    SlotSupplier
		getters  atomic.Int32
	}

	startupWorkflowSupplier struct {
		SlotSupplier
		hooks *startupHooks
	}

	startupPlugin struct {
		WorkerPluginBase
		ClientPluginBase
		start func(context.Context, WorkerPluginStartWorkerOptions, func(context.Context, WorkerPluginStartWorkerOptions) error) error
		stop  func(context.Context, WorkerPluginStopWorkerOptions, func(context.Context, WorkerPluginStopWorkerOptions))
		calls atomic.Int32
	}

	// The poller returns service errors only. The real base worker decides when
	// retrying ends, saves the fatal cause, and requests Stop.
	startupFatalPoller struct {
		worker  *AggregatedWorker
		started chan struct{}
		trigger *startupGate
		once    sync.Once
		cause   error
		// When configured, the second issued poll waits before returning its
		// error. It can finish after another poll has disabled new requests.
		second      *startupGate
		secondCause error
		pollCalls   atomic.Int32
	}

	// Before its Run call, the goroutine sends its own runtime header identity.
	// The test receives either that identity or a capture error before observing Run.
	startupRunIdentity struct {
		id  string
		err error
	}

	startupTask struct{}

	startupTaskPoller struct {
		stop  <-chan struct{}
		first atomic.Bool
	}

	startupTaskProcessor struct {
		processing *startupGate
	}
)

// Worker options create a real client and aggregate backed by typed mocks.
// Poll RPCs exit on shutdown or context cancellation; cleanup closes the client.
func newStartupFixture(t *testing.T, opts WorkerOptions, clientOpts ClientOptions, hooks *startupHooks) *startupFixture {
	t.Helper()
	if hooks == nil {
		hooks = &startupHooks{}
	}
	fixed, err := NewFixedSizeTuner(FixedSizeTunerOptions{
		NumWorkflowSlots: 4, NumActivitySlots: 4, NumLocalActivitySlots: 4, NumNexusSlots: 4,
	})
	require.NoError(t, err)
	tuner := &startupTuner{WorkerTuner: fixed, hooks: hooks, nexus: fixed.GetNexusSlotSupplier()}
	tuner.workflow = &startupWorkflowSupplier{SlotSupplier: fixed.GetWorkflowTaskSlotSupplier(), hooks: hooks}
	opts.Tuner = tuner
	clientOpts.Namespace = "startup-test-namespace"
	if clientOpts.WorkerHeartbeatInterval == 0 {
		clientOpts.WorkerHeartbeatInterval = -1
	}
	if hooks.skipOnly {
		clientOpts.Logger = &startupSkipLogger{hooks: hooks}
	} else {
		clientOpts.Logger = &startupLogger{hooks: hooks}
	}
	clientOpts.MetricsHandler = &startupMetrics{hooks: hooks}
	f := &startupFixture{
		hooks: hooks, tuner: tuner, pollStarted: make(chan struct{}),
		autoStop: true,
	}
	service := genworkflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *genworkflowservice.DescribeNamespaceRequest, ...grpc.CallOption) (*genworkflowservice.DescribeNamespaceResponse, error) {
			if f.describe != nil {
				f.describe()
			}
			return &genworkflowservice.DescribeNamespaceResponse{
				NamespaceInfo: &gennamespacepb.NamespaceInfo{
					Name: clientOpts.Namespace, State: genenumspb.NAMESPACE_STATE_REGISTERED,
					Capabilities: &gennamespacepb.NamespaceInfo_Capabilities{WorkerHeartbeats: f.heartbeats},
				},
				PollerGroupsInfo: f.groups,
			}, nil
		}).AnyTimes()
	poll := func(ctx context.Context) {
		f.pollOnce.Do(func() { close(f.pollStarted) })
		select {
		case <-f.worker.stopC:
		case <-ctx.Done():
		}
	}
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.PollActivityTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollActivityTaskQueueResponse, error) {
			poll(ctx)
			return &genworkflowservice.PollActivityTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.PollWorkflowTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollWorkflowTaskQueueResponse, error) {
			poll(ctx)
			return &genworkflowservice.PollWorkflowTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollNexusTaskQueueResponse, error) {
			poll(ctx)
			return &genworkflowservice.PollNexusTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().ShutdownWorker(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *genworkflowservice.ShutdownWorkerRequest, ...grpc.CallOption) (*genworkflowservice.ShutdownWorkerResponse, error) {
			f.shutdownCalls.Add(1)
			if f.shutdown != nil {
				f.shutdown.block()
			}
			return &genworkflowservice.ShutdownWorkerResponse{}, nil
		}).AnyTimes()
	service.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*genworkflowservice.RecordWorkerHeartbeatResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).AnyTimes()
	f.client = NewServiceClient(service, nil, clientOpts)
	f.worker = NewAggregatedWorker(f.client, "startup-test-queue", opts)
	f.worker.RegisterActivityWithOptions(func(context.Context) error { return nil }, RegisterActivityOptions{Name: "StartupActivity"})
	if !opts.DisableWorkflowWorker {
		f.worker.RegisterWorkflowWithOptions(func(Context) error { return nil }, RegisterWorkflowOptions{Name: "StartupWorkflow"})
	}
	t.Cleanup(func() {
		if f.shutdown != nil {
			f.shutdown.open()
		}
		if f.autoStop {
			f.worker.Stop()
		}
		f.client.Close()
	})
	return f
}

func startupOptions(disableWorkflow, sessions bool) WorkerOptions {
	behavior := NewPollerBehaviorSimpleMaximum(PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 2})
	return WorkerOptions{
		DisableWorkflowWorker: disableWorkflow, EnableSessionWorker: sessions,
		ActivityTaskPollerBehavior: behavior, WorkflowTaskPollerBehavior: behavior,
		NexusTaskPollerBehavior: behavior, WorkerStopTimeout: time.Minute,
	}
}

func startupOptionsWithPlugin(plugin WorkerPlugin) WorkerOptions {
	opts := startupOptions(true, false)
	opts.Plugins = []WorkerPlugin{plugin}
	return opts
}

func startupService(t *testing.T) *nexus.Service {
	t.Helper()
	service := nexus.NewService("StartupService")
	require.NoError(t, service.Register(nexus.NewSyncOperation(
		"operation", func(context.Context, string, nexus.StartOperationOptions) (string, error) {
			return "result", nil
		},
	)))
	return service
}

func newStartupGate(t *testing.T) *startupGate {
	t.Helper()
	gate := &startupGate{entered: make(chan struct{}), release: make(chan struct{})}
	gate.open = sync.OnceFunc(func() { close(gate.release) })
	t.Cleanup(gate.open)
	return gate
}

func startupStartAsync(worker *AggregatedWorker) <-chan error {
	result := make(chan error, 1)
	go func() { result <- worker.Start() }()
	return result
}

// When a test launches Run, the goroutine first reports its own runtime header
// number, then calls the real worker. The caller receives that number and a
// buffered result channel, so other tests' Run callers cannot supply its evidence.
func startupRunAsync(t *testing.T, worker *AggregatedWorker) (string, <-chan error) {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "Run identity capture requires the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	identity := make(chan startupRunIdentity, 1)
	result := make(chan error, 1)
	go func() {
		// A full four-MiB buffer cannot prove that the current stack is complete.
		// Report the failure without sending stack text or calling worker.Run.
		stack := make([]byte, 4194304)
		count := runtime.Stack(stack, false)
		if count == len(stack) {
			identity <- startupRunIdentity{err: fmt.Errorf("Run identity needs a complete stack snapshot")}
			return
		}
		header, _, complete := strings.Cut(string(stack[:count]), "\n")
		if !complete {
			identity <- startupRunIdentity{err: fmt.Errorf("Run identity is missing its runtime header")}
			return
		}
		id, _, err := startupRunHeader(header)
		identity <- startupRunIdentity{id: id, err: err}
		if err != nil {
			return
		}
		result <- worker.Run(nil)
	}()
	select {
	case captured := <-identity:
		require.NoError(t, captured.err, "Run identity capture failed")
		return captured.id, result
	case <-ctx.Done():
		t.Fatal("Run did not report its identity before the test deadline")
		return "", nil
	}
}

// A runtime header supplies a goroutine number and its current wait description.
// Validate both without reading stack arguments; the caller receives the exact
// number and description, or an error without the captured text.
func startupRunHeader(header string) (string, string, error) {
	if !strings.HasPrefix(header, "goroutine ") {
		return "", "", fmt.Errorf("Run header is missing the goroutine prefix")
	}
	id, description, ok := strings.Cut(strings.TrimPrefix(header, "goroutine "), " [")
	if !ok || len(id) == 0 || id[0] == '0' {
		return "", "", fmt.Errorf("Run header is missing a positive goroutine number")
	}
	for _, digit := range id {
		if digit < '0' || digit > '9' {
			return "", "", fmt.Errorf("Run header contains a nondecimal goroutine number")
		}
	}
	state, suffix, ok := strings.Cut(description, "]")
	if !ok || len(state) == 0 || suffix != ":" {
		return "", "", fmt.Errorf("Run header has an invalid wait description")
	}
	return id, state, nil
}

func startupRequireNoResult(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("startup caller returned while its synchronous work was held: %v", err)
	default:
	}
}

func startupContexts(worker *AggregatedWorker) []context.Context {
	var bases []*baseWorker
	if worker.workflowWorker != nil {
		bases = append(bases, worker.workflowWorker.localActivityWorker, worker.workflowWorker.worker)
	}
	if worker.activityWorker != nil {
		bases = append(bases, worker.activityWorker.worker)
	}
	if worker.sessionWorker != nil {
		bases = append(bases, worker.sessionWorker.creationWorker.worker, worker.sessionWorker.activityWorker.worker)
	}
	if worker.nexusWorker != nil {
		bases = append(bases, worker.nexusWorker.worker)
	}
	var contexts []context.Context
	for _, base := range bases {
		contexts = append(contexts, base.limiterContext, base.taskLimiterContext)
	}
	return contexts
}

func startupAssertCanceled(t *testing.T, worker *AggregatedWorker) {
	t.Helper()
	requireLifecycleClosed(t, worker.stopDone, "cleanup has not finished")
	for _, ctx := range startupContexts(worker) {
		requireLifecycleClosed(t, ctx.Done(), "a constructed child's context survived cleanup")
	}
}

func startupAssertNoMembership(t *testing.T, worker *AggregatedWorker) {
	t.Helper()
	dispatcher := worker.client.eagerDispatcher
	if dispatcher != nil {
		dispatcher.lock.RLock()
		assert.Empty(t, dispatcher.workersByTaskQueue[worker.executionParams.TaskQueue])
		dispatcher.lock.RUnlock()
	}
	manager := worker.client.heartbeatManager
	if manager != nil {
		manager.workersMutex.Lock()
		shared := manager.workers[worker.executionParams.Namespace]
		if shared != nil {
			shared.callbacksMutex.RLock()
			_, present := shared.callbacks[worker.workerInstanceKey]
			assert.False(t, present)
			shared.callbacksMutex.RUnlock()
		}
		manager.workersMutex.Unlock()
	}
}

func startupFatalCause(worker *AggregatedWorker) error {
	worker.fatalErrLock.Lock()
	defer worker.fatalErrLock.Unlock()
	return worker.fatalErr
}

func startupCacheReferences(worker *AggregatedWorker) int {
	lease := worker.cacheLease
	lease.lock.Lock()
	defer lease.lock.Unlock()
	return lease.sharedCache.workerRefcount
}

func startupAwaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "mutex fixtures require the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("operation did not reach its signal before the test deadline")
	}
}

func startupAwaitResult(t *testing.T, result <-chan error) error {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "mutex fixtures require the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("startup did not return before the test deadline")
		return nil
	}
}

// With Start held in its callback, the test uses the launched Run caller's
// goroutine number to observe its saved-Start wait before releasing poll errors.
func startupAwaitRunAttemptWait(t *testing.T, actorID string) {
	t.Helper()
	startupAwaitRunWait(t, actorID, "sync.Mutex.Lock", "sync.(*Once).doSlow")
}

// With Start's result received and Stop held in its RPC or plugin callback, the
// test observes that same launched Run caller waiting for complete SDK cleanup.
func startupAwaitRunCleanupWait(t *testing.T, actorID string) {
	t.Helper()
	startupAwaitRunWait(t, actorID, "chan receive", "go.temporal.io/sdk/internal.(*AggregatedWorker).Stop")
}

// For the launched Run caller's goroutine number, each complete snapshot must
// contain its wait header and required active frames in that same goroutine.
// Other Run callers supply no evidence. Missing evidence waits until the test
// deadline; duplicate, malformed or truncated evidence fails without a stack dump.
func startupAwaitRunWait(t *testing.T, actorID, waitState, waitFrame string) {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "Run observations require the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	// Four MiB bounds one stack-text snapshot, not total process memory or
	// enumeration time. A full buffer cannot prove that every actor was seen.
	stack := make([]byte, 4194304)
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("the one Run caller did not reach %s before the test deadline", waitState)
		default:
		}
		count := runtime.Stack(stack, true)
		require.Less(t, count, len(stack), "Run observation needs a complete stack snapshot")
		actors, waiting := 0, 0
		for _, goroutine := range strings.Split(string(stack[:count]), "\n\n") {
			header, body, ok := strings.Cut(goroutine, "\n")
			if !ok || !strings.HasPrefix(header, "goroutine "+actorID+" ") {
				continue
			}
			actors++
			id, state, err := startupRunHeader(header)
			require.NoError(t, err, "Run observation found a malformed target header")
			require.Equal(t, actorID, id, "Run observation selected a different caller")
			runFrame, callerFrame, targetFrame := false, false, false
			for _, line := range strings.Split(body, "\n") {
				// Only function lines before created-by metadata identify the
				// active call. Match the name before its argument list.
				if strings.HasPrefix(line, "created by ") {
					break
				}
				if strings.HasPrefix(line, "go.temporal.io/sdk/internal.(*AggregatedWorker).Run(") {
					runFrame = true
				}
				if strings.HasPrefix(line, "go.temporal.io/sdk/internal.startupRunAsync.func1(") {
					callerFrame = true
				}
				if strings.HasPrefix(line, waitFrame+"(") {
					targetFrame = true
				}
			}
			if runFrame && callerFrame && targetFrame &&
				(state == waitState || strings.HasPrefix(state, waitState+",")) {
				waiting++
			}
		}
		require.LessOrEqual(t, actors, 1, "Run observation found more than one relevant caller")
		if actors == 1 && waiting == 1 {
			return
		}
		runtime.Gosched()
	}
}

// This fixture holds only workersMutex and has exactly one startup goroutine.
// Its stack must show a mutex wait in registerWorker before Stop begins. This
// observes the existing SDK call without adding a production pause callback.
func startupAwaitManagerWait(t *testing.T) {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "mutex fixtures require the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	// The buffer holds at most four MiB of stack text, including other tests'
	// waiting goroutines. Truncation fails observation; it never means no waiter.
	stack := make([]byte, 4*1024*1024)
	for {
		select {
		case <-ctx.Done():
			t.Fatal("startup did not wait for the heartbeat manager before the test deadline")
		default:
		}
		count := runtime.Stack(stack, true)
		require.Less(t, count, len(stack), "goroutine evidence exceeded the fixture's bounded buffer")
		for _, goroutine := range strings.Split(string(stack[:count]), "\n\n") {
			if strings.Contains(goroutine, "[sync.Mutex.Lock]") &&
				strings.Contains(goroutine, "(*heartbeatManager).registerWorker(") &&
				strings.Contains(goroutine, "startupStartAsync.func1(") {
				return
			}
		}
		runtime.Gosched()
	}
}

// The preceding hook identifies startup's next operation and no Stop caller
// exists yet. TryLock observes ownership only; protected fields are never read
// on failure. The go-test deadline bounds observation and never releases work.
func startupAwaitLifecycleLock(t *testing.T, worker *AggregatedWorker) {
	t.Helper()
	deadline, ok := t.Deadline()
	require.True(t, ok, "mutex fixtures require the planned go-test -timeout")
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("startup did not acquire its resource mutex before the test deadline")
		default:
		}
		if !worker.lifecycleMu.TryLock() {
			return
		}
		worker.lifecycleMu.Unlock()
		runtime.Gosched()
	}
}

func (gate *startupGate) block() {
	gate.enter.Do(func() { close(gate.entered) })
	<-gate.release
}

func (hooks *startupHooks) hit(event string) {
	if !hooks.armed.Load() {
		return
	}
	hooks.mu.Lock()
	hooks.events = append(hooks.events, event)
	hooks.mu.Unlock()
	if hooks.onEvent != nil {
		hooks.onEvent(event)
	}
}

func (hooks *startupHooks) nexusEvents() []string {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	var events []string
	for _, event := range hooks.events {
		if strings.HasPrefix(event, "nexus.") || strings.HasPrefix(event, "NexusWorker.") {
			events = append(events, event)
		}
	}
	return events
}

func startupLogEvent(hooks *startupHooks, level, message string, fields []any) {
	workerType := ""
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == tagWorkerType {
			workerType, _ = fields[i+1].(string)
		}
	}
	switch {
	case level == "info" && message == "Started Worker" && workerType == "":
		hooks.hit("aggregate.started")
	case level == "info" && message == "Started Worker":
		if workerType == "ActivityWorker" {
			hooks.mu.Lock()
			hooks.activityLogs++
			ordinal := hooks.activityLogs
			hooks.mu.Unlock()
			workerType = fmt.Sprintf("%s.%d", workerType, ordinal)
		}
		hooks.hit(workerType + ".started")
	case level == "info" && message == "Starting session worker":
		hooks.hit("session.info")
	case level == "debug" && message == "Worker heartbeating configured, but server version does not support it.":
		hooks.hit("heartbeat.unsupported")
	case level == "error" && message == "Worker received non-retriable error. Shutting down.":
		if hooks.onFatal != nil {
			for i := 0; i+1 < len(fields); i += 2 {
				if fields[i] == tagError {
					hooks.onFatal(fields[i+1].(error))
				}
			}
		}
	}
}

func (logger *startupLogger) Debug(message string, fields ...any) {
	startupLogEvent(logger.hooks, "debug", message, append(slices.Clone(logger.fields), fields...))
}

func (logger *startupLogger) Info(message string, fields ...any) {
	startupLogEvent(logger.hooks, "info", message, append(slices.Clone(logger.fields), fields...))
}

func (logger *startupLogger) Warn(string, ...any) {}

func (logger *startupLogger) Error(message string, fields ...any) {
	startupLogEvent(logger.hooks, "error", message, append(slices.Clone(logger.fields), fields...))
}

func (logger *startupLogger) With(fields ...any) log.Logger {
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == tagWorkerType && fields[i+1] == "NexusWorker" {
			logger.hooks.hit("nexus.logger.with")
		}
	}
	return &startupLogger{hooks: logger.hooks, fields: append(slices.Clone(logger.fields), fields...)}
}

func (logger *startupSkipLogger) Debug(message string, fields ...any) {
	startupLogEvent(logger.hooks, "debug", message, fields)
}

func (logger *startupSkipLogger) Info(message string, fields ...any) {
	startupLogEvent(logger.hooks, "info", message, fields)
}

func (logger *startupSkipLogger) Warn(string, ...any) {}

func (logger *startupSkipLogger) Error(message string, fields ...any) {
	startupLogEvent(logger.hooks, "error", message, fields)
}

func (logger *startupSkipLogger) WithCallerSkip(int) log.Logger {
	if logger.hooks.nexusPreparing.Load() {
		logger.hooks.hit("nexus.logger.skip")
	}
	return &startupSkipLogger{hooks: logger.hooks}
}

func (handler *startupMetrics) WithTags(tags map[string]string) metrics.Handler {
	merged := make(map[string]string, len(handler.tags)+len(tags))
	for key, value := range handler.tags {
		merged[key] = value
	}
	for key, value := range tags {
		merged[key] = value
	}
	if tags[metrics.PollerTypeTagName] == metrics.PollerTypeNexusTask {
		handler.hooks.hit("nexus.poller.tags")
	}
	if tags[metrics.WorkerTypeTagName] == "NexusWorker" {
		handler.hooks.hit("nexus.worker.tags")
	}
	return &startupMetrics{hooks: handler.hooks, tags: merged}
}

func (handler *startupMetrics) Counter(name string) metrics.Counter {
	if name != metrics.WorkerStartCounter {
		return metrics.CounterFunc(func(int64) {})
	}
	workerType := handler.tags[metrics.WorkerTypeTagName]
	if workerType == "ActivityWorker" {
		handler.hooks.mu.Lock()
		handler.hooks.activity++
		ordinal := handler.hooks.activity
		handler.hooks.mu.Unlock()
		workerType = fmt.Sprintf("%s.%d", workerType, ordinal)
	}
	handler.hooks.hit(workerType + ".counter")
	return metrics.CounterFunc(func(int64) { handler.hooks.hit(workerType + ".inc") })
}

func (handler *startupMetrics) Gauge(name string) metrics.Gauge {
	if handler.tags[metrics.WorkerTypeTagName] == "NexusWorker" {
		switch name {
		case metrics.WorkerTaskSlotsAvailable:
			handler.hooks.hit("nexus.available.gauge")
		case metrics.WorkerTaskSlotsUsed:
			handler.hooks.hit("nexus.used.gauge")
		}
	} else if name == metrics.NumPoller && handler.tags[metrics.PollerTypeTagName] == metrics.PollerTypeNexusTask {
		handler.hooks.hit("nexus.poller.gauge")
	}
	return metrics.GaugeFunc(func(float64) {})
}

func (*startupMetrics) Timer(string) metrics.Timer {
	return metrics.TimerFunc(func(time.Duration) {})
}

func (tuner *startupTuner) GetWorkflowTaskSlotSupplier() SlotSupplier {
	return tuner.workflow
}

func (tuner *startupTuner) GetNexusSlotSupplier() SlotSupplier {
	tuner.getters.Add(1)
	tuner.hooks.hit("nexus.getter")
	tuner.hooks.nexusPreparing.Store(true)
	return tuner.nexus
}

func (supplier *startupWorkflowSupplier) MaxSlots() int {
	supplier.hooks.hit("workflow.max_slots")
	return supplier.SlotSupplier.MaxSlots()
}

func (*startupPlugin) Name() string {
	return "startup-admission-fixture"
}

func (plugin *startupPlugin) StartWorker(ctx context.Context, in WorkerPluginStartWorkerOptions, next func(context.Context, WorkerPluginStartWorkerOptions) error) error {
	plugin.calls.Add(1)
	if plugin.start != nil {
		return plugin.start(ctx, in, next)
	}
	return next(ctx, in)
}

func (plugin *startupPlugin) StopWorker(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
	if plugin.stop != nil {
		plugin.stop(ctx, in, next)
		return
	}
	next(ctx, in)
}

func (poller *startupFatalPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
	poller.once.Do(func() { close(poller.started) })
	if poller.second != nil && poller.pollCalls.Add(1) == 2 {
		poller.second.block()
		return nil, poller.secondCause
	}
	select {
	case <-poller.worker.stopC:
		return nil, nil
	case <-poller.trigger.release:
		return nil, poller.cause
	}
}

func (*startupTask) isEmpty() bool {
	return false
}

func (*startupTask) scaleDecision() (pollerScaleDecision, bool) {
	return pollerScaleDecision{}, false
}

func (poller *startupTaskPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
	if poller.first.CompareAndSwap(false, true) {
		return &startupTask{}, nil
	}
	<-poller.stop
	return nil, nil
}

func (processor *startupTaskProcessor) ProcessTask(any) error {
	processor.processing.block()
	return nil
}
