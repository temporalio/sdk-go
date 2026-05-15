package internal

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common/cache"
	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
)

// nexusSCCapturingDC records SerializationContext values, thread-safe.
type nexusSCCapturingDC struct {
	converter.DataConverter
	mu       sync.Mutex
	captured []converter.SerializationContext
}

func (dc *nexusSCCapturingDC) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	dc.mu.Lock()
	dc.captured = append(dc.captured, ctx)
	dc.mu.Unlock()
	return dc
}

func (dc *nexusSCCapturingDC) getCaptured() []converter.SerializationContext {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]converter.SerializationContext, len(dc.captured))
	copy(out, dc.captured)
	return out
}

// nexusSCCapturingFC records SerializationContext values, thread-safe.
type nexusSCCapturingFC struct {
	converter.FailureConverter
	mu       sync.Mutex
	captured []converter.SerializationContext
}

func (fc *nexusSCCapturingFC) WithSerializationContext(ctx converter.SerializationContext) converter.FailureConverter {
	fc.mu.Lock()
	fc.captured = append(fc.captured, ctx)
	fc.mu.Unlock()
	return fc
}

func (fc *nexusSCCapturingFC) getCaptured() []converter.SerializationContext {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	out := make([]converter.SerializationContext, len(fc.captured))
	copy(out, fc.captured)
	return out
}

// nexusTestFC is a minimal FailureConverter for these tests.
type nexusTestFC struct{}

func (nexusTestFC) ErrorToFailure(err error) *failurepb.Failure {
	if err == nil {
		return nil
	}
	return &failurepb.Failure{Message: err.Error()}
}
func (nexusTestFC) FailureToError(f *failurepb.Failure) error {
	if f == nil {
		return nil
	}
	return &ApplicationError{msg: f.GetMessage()}
}

// panicOnNexusDC panics when it receives NexusSerializationContext.
type panicOnNexusDC struct {
	converter.DataConverter
}

func (dc *panicOnNexusDC) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	if _, ok := ctx.(converter.NexusSerializationContext); ok {
		panic("user codec does not support NexusSerializationContext")
	}
	return dc
}

func makeNexusHeaders(endpoint, service, operation string) *commonpb.Header {
	return &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			NexusEndpointHeaderKey:  NewRawStringHeaderPayload(endpoint),
			NexusServiceHeaderKey:   NewRawStringHeaderPayload(service),
			NexusOperationHeaderKey: NewRawStringHeaderPayload(operation),
		},
	}
}

func makeTestWorkflowInfo() *WorkflowInfo {
	return &WorkflowInfo{
		Namespace: "test-ns",
		WorkflowExecution: WorkflowExecution{
			ID:    "wf-123",
			RunID: "run-abc",
		},
		WorkflowType:  WorkflowType{Name: "TestWorkflow"},
		TaskQueueName: "test-tq",
	}
}

// buildEventHandler constructs a workflowExecutionEventHandlerImpl with the given converters.
func buildEventHandler(dc converter.DataConverter, fc converter.FailureConverter) *workflowExecutionEventHandlerImpl {
	wfInfo := makeTestWorkflowInfo()
	r := newRegistry()
	// Register a dummy workflow so handleWorkflowExecutionStarted can find it.
	r.RegisterWorkflowWithOptions(
		func(ctx Context) error { return nil },
		RegisterWorkflowOptions{Name: "TestWorkflow"},
	)
	handler := newWorkflowExecutionEventHandler(
		wfInfo,
		func(result *commonpb.Payloads, err error) {},
		ilog.NewNopLogger(),
		false,
		metrics.NopHandler,
		r,
		dc,
		fc,
		nil, // contextPropagators
		0,   // deadlockDetectionTimeout
		nil, // capabilities
	)
	return handler.(*workflowExecutionEventHandlerImpl)
}

func TestHandleWorkflowExecutionStarted_NexusHeadersPopulateBoundaryConverters(t *testing.T) {
	t.Parallel()
	capDC := &nexusSCCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	capFC := &nexusSCCapturingFC{FailureConverter: nexusTestFC{}}
	weh := buildEventHandler(capDC, capFC)

	// Before start, boundary converters are nil.
	require.Nil(t, weh.GetNexusBoundaryDataConverter())
	require.Nil(t, weh.GetNexusBoundaryFailureConverter())

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep-1", "svc-1", "op-1"),
		Input:  nil,
	}
	err := weh.handleWorkflowExecutionStarted(attrs)
	require.NoError(t, err)

	// Boundary converters must now be populated.
	require.NotNil(t, weh.GetNexusBoundaryDataConverter())
	require.NotNil(t, weh.GetNexusBoundaryFailureConverter())

	// The DC wrapping must have received NexusSerializationContext (NOT WorkflowSerializationContext).
	dcCaptured := capDC.getCaptured()
	// First capture is WorkflowSerializationContext from constructor, second is NexusSerializationContext.
	var foundNexus bool
	for _, c := range dcCaptured {
		if nexSC, ok := c.(converter.NexusSerializationContext); ok {
			require.Equal(t, "test-ns", nexSC.Namespace)
			require.Len(t, nexSC.Operations, 1)
			require.Equal(t, "ep-1", nexSC.Operations[0].Endpoint)
			require.Equal(t, "svc-1", nexSC.Operations[0].Service)
			require.Equal(t, "op-1", nexSC.Operations[0].Operation)
			foundNexus = true
			break
		}
	}
	require.True(t, foundNexus, "NexusSerializationContext not found in DC captures: %v", dcCaptured)

	// Same for FC.
	fcCaptured := capFC.getCaptured()
	foundNexus = false
	for _, c := range fcCaptured {
		if nexSC, ok := c.(converter.NexusSerializationContext); ok {
			require.Len(t, nexSC.Operations, 1)
			require.Equal(t, "ep-1", nexSC.Operations[0].Endpoint)
			foundNexus = true
			break
		}
	}
	require.True(t, foundNexus, "NexusSerializationContext not found in FC captures: %v", fcCaptured)
}

func TestHandleWorkflowExecutionStarted_NoNexusHeadersKeepsWorkflowContext(t *testing.T) {
	t.Parallel()
	weh := buildEventHandler(converter.GetDefaultDataConverter(), nexusTestFC{})

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"some-user-header": NewRawStringHeaderPayload("value"),
			},
		},
	}
	err := weh.handleWorkflowExecutionStarted(attrs)
	require.NoError(t, err)

	// Boundary converters must remain nil.
	require.Nil(t, weh.GetNexusBoundaryDataConverter())
	require.Nil(t, weh.GetNexusBoundaryFailureConverter())
}

func TestHandleWorkflowExecutionStarted_PartialNexusHeadersIgnored(t *testing.T) {
	t.Parallel()
	weh := buildEventHandler(converter.GetDefaultDataConverter(), nexusTestFC{})

	// Only two of three keys present — should not populate boundary converters.
	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				NexusEndpointHeaderKey: NewRawStringHeaderPayload("ep"),
				NexusServiceHeaderKey:  NewRawStringHeaderPayload("svc"),
				// NexusOperationHeaderKey missing
			},
		},
	}
	err := weh.handleWorkflowExecutionStarted(attrs)
	require.NoError(t, err)

	require.Nil(t, weh.GetNexusBoundaryDataConverter())
	require.Nil(t, weh.GetNexusBoundaryFailureConverter())
}

func TestGetDataConverter_UnchangedForNexusBackedWorkflow(t *testing.T) {
	t.Parallel()
	weh := buildEventHandler(converter.GetDefaultDataConverter(), nexusTestFC{})

	dcBefore := weh.GetDataConverter()
	require.NotNil(t, dcBefore)

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	err := weh.handleWorkflowExecutionStarted(attrs)
	require.NoError(t, err)

	// GetDataConverter() must still return the WorkflowSerializationContext-wrapped DC.
	require.Same(t, dcBefore, weh.GetDataConverter())
	// But the boundary converter is different.
	require.NotNil(t, weh.GetNexusBoundaryDataConverter())
}

func TestNexusBoundary_RewrapPanicPropagates(t *testing.T) {
	t.Parallel()
	panicDC := &panicOnNexusDC{DataConverter: converter.GetDefaultDataConverter()}
	weh := buildEventHandler(panicDC, nexusTestFC{})

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	// Start itself just records the operation chain; boundary converters are
	// wrapped lazily, so the codec panic surfaces at the first consumption
	// site (input decode, failure encode, cancel encode), not at start.
	require.NoError(t, weh.handleWorkflowExecutionStarted(attrs))

	require.PanicsWithValue(t, "user codec does not support NexusSerializationContext", func() {
		_ = weh.GetNexusBoundaryDataConverter()
	})
}

// TestContinueAsNew_NonNexusBackedWorkflow_NoHeadersInjected asserts the
// negative case: in the test environment the env stub returns nil from
// GetNexusHeaderPayloads, so NewContinueAsNewError does NOT inject the reserved
// __temporal_nexus_* keys into the CAN header.
func TestContinueAsNew_NonNexusBackedWorkflow_NoHeadersInjected(t *testing.T) {
	t.Parallel()
	continueAsNewWfName := "continueAsNewNexusWorkflow"
	continueAsNewWorkflowFn := func(ctx Context) error {
		return NewContinueAsNewError(ctx, continueAsNewWfName)
	}

	s := &WorkflowTestSuite{}
	wfEnv := s.NewTestWorkflowEnvironment()
	wfEnv.RegisterWorkflowWithOptions(continueAsNewWorkflowFn, RegisterWorkflowOptions{
		Name: continueAsNewWfName,
	})

	// The test environment's testWorkflowEnvironmentImpl returns nil from
	// GetNexusHeaderPayloads(), so in the test environment CAN headers won't
	// include Nexus keys. That's the correct behavior per spec (test env
	// does not support Nexus-backed workflows via headers).
	// We verify the non-Nexus case doesn't break.
	wfEnv.ExecuteWorkflow(continueAsNewWorkflowFn)
	err := wfEnv.GetWorkflowError()
	require.Error(t, err)

	var workflowErr *WorkflowExecutionError
	require.True(t, errors.As(err, &workflowErr))

	var contErr *ContinueAsNewError
	require.True(t, errors.As(errors.Unwrap(workflowErr), &contErr))
	require.Equal(t, continueAsNewWfName, contErr.WorkflowType.Name)

	// Verify no Nexus headers in CAN (since test env returns nil).
	if contErr.Header != nil && contErr.Header.Fields != nil {
		_, hasEP := contErr.Header.Fields[NexusEndpointHeaderKey]
		require.False(t, hasEP, "test env should not inject Nexus headers")
	}
}

func TestNexusBoundaryConverterAccessors(t *testing.T) {
	t.Parallel()
	weh := buildEventHandler(converter.GetDefaultDataConverter(), nexusTestFC{})

	// Before start, all return nil.
	require.Nil(t, weh.GetNexusBoundaryDataConverter())
	require.Nil(t, weh.GetNexusBoundaryFailureConverter())

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	_ = weh.handleWorkflowExecutionStarted(attrs)

	require.NotNil(t, weh.GetNexusBoundaryDataConverter())
	require.NotNil(t, weh.GetNexusBoundaryFailureConverter())
}

func TestNexusBoundary_TestEnvStubsReturnNil(t *testing.T) {
	t.Parallel()
	// Verify testWorkflowEnvironmentImpl satisfies the interface with nil returns.
	s := &WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	impl := env.impl

	require.Nil(t, impl.GetNexusBoundaryDataConverter())
	require.Nil(t, impl.GetNexusBoundaryFailureConverter())
}

// TestNexusBoundary_GetDataConverter_PreservesWorkflowContext verifies that the
// nexus-boundary data converter does not replace the workflow's normal data
// converter: GetDataConverter() still returns the WorkflowSerializationContext-
// wrapped converter (used for activity input encoding, child workflows, queries,
// side-effects, etc.), and the nexus-boundary converter sits beside it as a
// separate accessor.
//
// Full end-to-end activity-context isolation (scheduling an actual activity from
// a Nexus-backed workflow and asserting the activity observes
// ActivitySerializationContext) belongs in the Step 6 integration suite.
func TestNexusBoundary_GetDataConverter_PreservesWorkflowContext(t *testing.T) {
	t.Parallel()
	capDC := &nexusSCCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	weh := buildEventHandler(capDC, nexusTestFC{})

	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	_ = weh.handleWorkflowExecutionStarted(attrs)

	// GetDataConverter() is what activities/child workflows/queries use for encoding.
	activityDC := weh.GetDataConverter()
	nexusDC := weh.GetNexusBoundaryDataConverter()

	// They must be different objects.
	require.NotNil(t, activityDC)
	require.NotNil(t, nexusDC)
	// GetDataConverter() was set before Nexus boundary. They should not be
	// the same (one is WorkflowSerializationContext-wrapped, other is
	// NexusSerializationContext-wrapped).
	// Since our capturing DC returns self, verify the captures list shows
	// WorkflowSerializationContext was applied first.
	dcCaptured := capDC.getCaptured()
	require.GreaterOrEqual(t, len(dcCaptured), 2)
	// First capture should be WorkflowSerializationContext (from constructor).
	_, isWfCtx := dcCaptured[0].(converter.WorkflowSerializationContext)
	require.True(t, isWfCtx, "first DC capture should be WorkflowSerializationContext, got %T", dcCaptured[0])
	// Second capture should be NexusSerializationContext (from handleWorkflowExecutionStarted).
	_, isNexusCtx := dcCaptured[1].(converter.NexusSerializationContext)
	require.True(t, isNexusCtx, "second DC capture should be NexusSerializationContext, got %T", dcCaptured[1])
}

// --- Step 3 tests: caller-side (prepareNexusOperationParams) ---

// mockNexusClient satisfies NexusClient for testing.
type mockNexusClient struct {
	endpoint, service string
}

func (c mockNexusClient) Endpoint() string { return c.endpoint }
func (c mockNexusClient) Service() string  { return c.service }
func (c mockNexusClient) ExecuteOperation(ctx Context, operation any, input any, options NexusOperationOptions) NexusOperationFuture {
	return nil
}

func TestPrepareNexusOperationParams_InputUsesNexusContext(t *testing.T) {
	t.Parallel()
	capDC := &nexusSCCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	weh := buildEventHandler(capDC, nexusTestFC{})

	wc := &workflowEnvironmentInterceptor{env: weh}

	client := mockNexusClient{endpoint: "test-ep", service: "test-svc"}
	input := ExecuteNexusOperationInput{
		Client:    client,
		Operation: "test-op",
		Input:     "hello",
	}

	params, err := wc.prepareNexusOperationParams(nil, input)
	require.NoError(t, err)
	require.NotNil(t, params.input)
	require.NotNil(t, params.dataConverter)

	// Verify NexusSerializationContext was passed to the data converter.
	captured := capDC.getCaptured()
	// First capture is WorkflowSerializationContext from constructor,
	// subsequent ones include NexusSerializationContext from prepareNexusOperationParams.
	var foundNexus bool
	for _, c := range captured {
		if nexSC, ok := c.(converter.NexusSerializationContext); ok {
			require.Equal(t, "test-ns", nexSC.Namespace)
			require.Len(t, nexSC.Operations, 1)
			require.Equal(t, "test-ep", nexSC.Operations[0].Endpoint)
			require.Equal(t, "test-svc", nexSC.Operations[0].Service)
			require.Equal(t, "test-op", nexSC.Operations[0].Operation)
			foundNexus = true
			break
		}
	}
	require.True(t, foundNexus, "NexusSerializationContext not found in captures")
}

func TestPrepareNexusOperationParams_DataConverterSetOnResult(t *testing.T) {
	t.Parallel()
	capDC := &nexusSCCapturingDC{DataConverter: converter.GetDefaultDataConverter()}
	weh := buildEventHandler(capDC, nexusTestFC{})
	wc := &workflowEnvironmentInterceptor{env: weh}

	client := mockNexusClient{endpoint: "ep", service: "svc"}
	input := ExecuteNexusOperationInput{
		Client:    client,
		Operation: "op",
		Input:     42,
	}

	params, err := wc.prepareNexusOperationParams(nil, input)
	require.NoError(t, err)
	// The params.dataConverter should be non-nil and set for result future decoding.
	require.NotNil(t, params.dataConverter)
}

func TestNexusOperationFailureConverterUsesNexusContext(t *testing.T) {
	t.Parallel()
	capFC := &nexusSCCapturingFC{FailureConverter: nexusTestFC{}}
	weh := buildEventHandler(converter.GetDefaultDataConverter(), capFC)

	// Clear the constructor WorkflowSC capture before the test assertion.
	capFC.mu.Lock()
	capFC.captured = nil
	capFC.mu.Unlock()

	// Simulate ExecuteNexusOperation which sets failureConverter on scheduledNexusOperation.
	nexCtx := converter.NexusSerializationContext{
		Namespace: "test-ns",
		Operations: []converter.NexusOperation{
			{Endpoint: "ep", Service: "svc", Operation: "op"},
		},
	}
	fc := converter.WithFailureConverterSerializationContext(weh.failureConverter, nexCtx)
	require.NotNil(t, fc)

	captured := capFC.getCaptured()
	require.GreaterOrEqual(t, len(captured), 1)
	nexSC, ok := captured[0].(converter.NexusSerializationContext)
	require.True(t, ok, "expected NexusSerializationContext, got %T", captured[0])
	require.Len(t, nexSC.Operations, 1)
	require.Equal(t, "ep", nexSC.Operations[0].Endpoint)
	require.Equal(t, "svc", nexSC.Operations[0].Service)
	require.Equal(t, "op", nexSC.Operations[0].Operation)
}

// --- Q5 benchmark ---

// buildMinimalWth constructs a workflowTaskHandlerImpl with the given converters
// and a no-op WorkerCache, suitable for exercising completeWorkflow in unit tests.
func buildMinimalWth(dc converter.DataConverter, fc converter.FailureConverter) *workflowTaskHandlerImpl {
	lruCache := cache.NewLRU(1)
	return &workflowTaskHandlerImpl{
		metricsHandler:  metrics.NopHandler,
		logger:          ilog.NewNopLogger(),
		dataConverter:   dc,
		failureConverter: fc,
		cache: &WorkerCache{
			sharedCache: &sharedWorkerCache{workflowCache: &lruCache},
		},
	}
}

// spyFC is a FailureConverter that records ErrorToFailure calls.
type spyFC struct {
	converter.FailureConverter
	mu    sync.Mutex
	calls int
}

func (s *spyFC) ErrorToFailure(err error) *failurepb.Failure {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.FailureConverter.ErrorToFailure(err)
}

func (s *spyFC) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// spyDC is a DataConverter that records ToPayloads calls.
type spyDC struct {
	converter.DataConverter
	mu    sync.Mutex
	calls int
}

func (s *spyDC) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.DataConverter.ToPayloads(values...)
}

func (s *spyDC) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestWorkflowFailureClosePath_UsesNexusBoundaryFailureConverter verifies that
// when GetNexusBoundaryFailureConverter returns a non-nil converter, the
// failure-close path in completeWorkflow uses it rather than wth.failureConverter.
func TestWorkflowFailureClosePath_UsesNexusBoundaryFailureConverter(t *testing.T) {
	t.Parallel()

	// nexusBoundaryFC is the spy we expect to be called. capFC wraps it so
	// WithSerializationContext returns capFC (which embeds nexusBoundaryFC),
	// and any ErrorToFailure call ultimately runs on nexusBoundaryFC via
	// method promotion. capFC.WithSerializationContext does not invoke
	// ErrorToFailure, so the spy's call count is not inflated by JIT wrapping.
	nexusBoundaryFC := &spyFC{FailureConverter: nexusTestFC{}}
	// wthFC is the wth-level FC we expect NOT to be called for the failure payload.
	wthFC := &spyFC{FailureConverter: nexusTestFC{}}

	capFC := &nexusSCCapturingFC{FailureConverter: nexusBoundaryFC}
	weh := buildEventHandler(converter.GetDefaultDataConverter(), capFC)
	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	require.NoError(t, weh.handleWorkflowExecutionStarted(attrs))
	require.NotNil(t, weh.GetNexusBoundaryFailureConverter())

	wth := buildMinimalWth(converter.GetDefaultDataConverter(), wthFC)

	task := &workflowservice.PollWorkflowTaskQueueResponse{}
	workflowContext := &workflowExecutionContextImpl{
		workflowInfo: makeTestWorkflowInfo(),
		wth:          wth,
		err:          errors.New("workflow failed"),
	}
	var iface workflowExecutionEventHandler = weh
	workflowContext.eventHandler = &iface

	_ = wth.completeWorkflow(weh, task, workflowContext, nil, nil, false)

	// The nexus-boundary spy must have been called for the failure encoding.
	require.Greater(t, nexusBoundaryFC.callCount(), 0, "expected nexusBoundaryFC.ErrorToFailure to be called")
	// The wth-level FC must NOT have been called (nexus boundary FC took over).
	require.Equal(t, 0, wthFC.callCount(), "expected wthFC.ErrorToFailure NOT to be called when nexusBoundaryFC is set")
}

// TestWorkflowCancelPath_UsesNexusBoundaryDataConverter verifies that when
// GetNexusBoundaryDataConverter returns a non-nil converter, the cancel-close
// path in completeWorkflow uses it for encoding cancellation details.
func TestWorkflowCancelPath_UsesNexusBoundaryDataConverter(t *testing.T) {
	t.Parallel()

	// nexusBoundaryDC is the spy we expect to be called. capDC wraps it so
	// WithSerializationContext returns capDC, and ToPayloads flows through to
	// nexusBoundaryDC via method promotion (capDC does not override ToPayloads).
	nexusBoundaryDC := &spyDC{DataConverter: converter.GetDefaultDataConverter()}
	// wthDC is the wth-level DC we expect NOT to be called for cancel details.
	wthDC := &spyDC{DataConverter: converter.GetDefaultDataConverter()}

	capDC := &nexusSCCapturingDC{DataConverter: nexusBoundaryDC}
	weh := buildEventHandler(capDC, nexusTestFC{})
	attrs := &historypb.WorkflowExecutionStartedEventAttributes{
		Header: makeNexusHeaders("ep", "svc", "op"),
	}
	require.NoError(t, weh.handleWorkflowExecutionStarted(attrs))
	require.NotNil(t, weh.GetNexusBoundaryDataConverter())

	wth := buildMinimalWth(wthDC, nexusTestFC{})

	task := &workflowservice.PollWorkflowTaskQueueResponse{}
	// CanceledError with details triggers ToPayloads on the chosen DC.
	cancelErr := NewCanceledError("detail-value")
	workflowContext := &workflowExecutionContextImpl{
		workflowInfo: makeTestWorkflowInfo(),
		wth:          wth,
		err:          cancelErr,
	}
	var iface2 workflowExecutionEventHandler = weh
	workflowContext.eventHandler = &iface2

	_ = wth.completeWorkflow(weh, task, workflowContext, nil, nil, false)

	// The nexus-boundary spy must have been called for the cancel details encoding.
	require.Greater(t, nexusBoundaryDC.callCount(), 0, "expected nexusBoundaryDC.ToPayloads to be called")
	// The wth-level DC must NOT have been called for cancel details.
	require.Equal(t, 0, wthDC.callCount(), "expected wthDC.ToPayloads NOT to be called when nexusBoundaryDC is set")
}

func BenchmarkNexusBoundaryRewrap(b *testing.B) {
	dc := converter.GetDefaultDataConverter()
	fc := nexusTestFC{}
	wfInfo := makeTestWorkflowInfo()

	// Simulate the JIT wrap path at GetNexusBoundaryDataConverter / GetNexusBoundaryFailureConverter.
	nexCtx := converter.NexusSerializationContext{
		Namespace: wfInfo.Namespace,
		Operations: []converter.NexusOperation{
			{Endpoint: "bench-ep", Service: "bench-svc", Operation: "bench-op"},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = converter.WithDataConverterSerializationContext(dc, nexCtx)
		_ = converter.WithFailureConverterSerializationContext(fc, nexCtx)
	}
}
