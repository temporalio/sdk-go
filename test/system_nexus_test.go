package test_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"
)

// -----------------------------------------------------------------------------
// TEST SUITE AND HELPERS
// -----------------------------------------------------------------------------

type SystemNexusTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker worker.Worker
}

func TestSystemNexusSuite(t *testing.T) {
	suite.Run(t, new(SystemNexusTestSuite))
}

func (ts *SystemNexusTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	skipOnCloud(ts.T(), cloudRequiresProvisioning, "system Nexus signal-with-start is not enabled in Cloud test namespaces")
	if os.Getenv("DISABLE_SYSTEM_NEXUS_TESTS") != "" {
		ts.T().Skip("server does not support system Nexus signal-with-start")
	}
	ts.initConfig()
	ts.NoError(ts.InitClient(func(options *client.Options) {
		options.DataConverter = converter.NewCodecDataConverter(
			converter.GetDefaultDataConverter(), &intTestSigningCodec{},
		)
	}))
	ts.NoError(ts.InitNamespace())
}

func (ts *SystemNexusTestSuite) SetupTest() {
	ts.Assertions = require.New(ts.T())
	worker.SetStickyWorkflowCacheSize(ts.config.maxWorkflowCacheSize)
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		WorkflowPanicPolicy: worker.FailWorkflow,
	})
	ts.worker.RegisterWorkflow(RepeatedlySignalAndStart)
	ts.worker.RegisterWorkflow(SignalAndStartWithLotsOfOptions)
	ts.worker.RegisterWorkflow(SignalAndStartWithCron)
	ts.worker.RegisterWorkflow(RepeatedlyReceiveSignal)
	ts.NoError(ts.worker.Start())
}

func (ts *SystemNexusTestSuite) TearDownTest() {
	if ts.worker != nil {
		ts.worker.Stop()
		ts.worker = nil
	}
}

func (ts *SystemNexusTestSuite) TearDownSuite() {
	if ts.client != nil {
		ts.client.Close()
		ts.client = nil
	}
}

// signalWithStartRequestFromHistory gets the request from history, assuming
// the caller scheduled exactly one signal-with-start operation.
func (ts *SystemNexusTestSuite) signalWithStartRequestFromHistory(
	ctx context.Context, callerID string,
) *workflowservice.SignalWithStartWorkflowExecutionRequest {
	history := ts.client.GetWorkflowHistory(ctx, callerID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var request *workflowservice.SignalWithStartWorkflowExecutionRequest
	for history.HasNext() {
		event, err := history.Next()
		ts.NoError(err)
		if scheduled := event.GetNexusOperationScheduledEventAttributes(); scheduled != nil {
			ts.Nil(request, "caller must schedule only one signal-with-start operation")
			request = &workflowservice.SignalWithStartWorkflowExecutionRequest{}
			ts.NoError(proto.Unmarshal(scheduled.GetInput().GetData(), request))
		}
	}
	ts.NotNil(request, "caller must schedule a signal-with-start operation")
	return request
}

// -----------------------------------------------------------------------------
// WORKFLOWS
// -----------------------------------------------------------------------------

func RepeatedlySignalAndStart(
	ctx workflow.Context,
	calleeID string,
	count int,
) ([]workflow.SignalWithStartWorkflowResponse, error) {
	var responses []workflow.SignalWithStartWorkflowResponse
	for i := range count {
		var response workflow.SignalWithStartWorkflowResponse
		err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
			ID:                       calleeID,
			TaskQueue:                workflow.GetInfo(ctx).TaskQueueName,
			WorkflowExecutionTimeout: 20 * time.Second,
		}, "signal", i, RepeatedlyReceiveSignal, count).Get(ctx, &response)
		if err != nil {
			return nil, err
		}
		responses = append(responses, response)
	}
	return responses, nil
}

type LotsOfOptions struct {
	ID                       string
	TaskQueue                string
	WorkflowExecutionTimeout time.Duration
	WorkflowRunTimeout       time.Duration
	WorkflowTaskTimeout      time.Duration
	WorkflowIDReusePolicy    enumspb.WorkflowIdReusePolicy
	WorkflowIDConflictPolicy enumspb.WorkflowIdConflictPolicy
	RetryPolicy              *temporal.RetryPolicy
	Memo                     map[string]any
	KeywordSearchField       string
	KeywordSearchAttribute   string
	Priority                 *temporal.Priority
	// Use a concrete override type so it round-trips through workflow arguments.
	VersioningOverride client.AutoUpgradeVersioningOverride
	StartDelay         time.Duration
	StaticSummary      string
	StaticDetails      string
}

func SignalAndStartWithLotsOfOptions(ctx workflow.Context, options LotsOfOptions) (string, error) {
	var response workflow.SignalWithStartWorkflowResponse
	err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
		ID:                       options.ID,
		TaskQueue:                options.TaskQueue,
		WorkflowExecutionTimeout: options.WorkflowExecutionTimeout,
		WorkflowRunTimeout:       options.WorkflowRunTimeout,
		WorkflowTaskTimeout:      options.WorkflowTaskTimeout,
		WorkflowIDReusePolicy:    options.WorkflowIDReusePolicy,
		WorkflowIDConflictPolicy: options.WorkflowIDConflictPolicy,
		RetryPolicy:              options.RetryPolicy,
		Memo:                     options.Memo,
		TypedSearchAttributes: temporal.NewSearchAttributes(
			temporal.NewSearchAttributeKeyKeyword(options.KeywordSearchField).
				ValueSet(options.KeywordSearchAttribute),
		),
		Priority:           options.Priority,
		VersioningOverride: &options.VersioningOverride,
		StartDelay:         options.StartDelay,
		StaticSummary:      options.StaticSummary,
		StaticDetails:      options.StaticDetails,
	}, "signal", 0, RepeatedlyReceiveSignal, 1).Get(ctx, &response)
	if err != nil {
		return "", err
	}
	return *response.RunID, nil
}

func SignalAndStartWithCron(
	ctx workflow.Context, calleeID string,
) (string, error) {
	var response workflow.SignalWithStartWorkflowResponse
	err := workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{
		ID:                       calleeID,
		TaskQueue:                workflow.GetInfo(ctx).TaskQueueName,
		WorkflowExecutionTimeout: 20 * time.Second,
		CronSchedule:             "@every 1s",
	}, "signal", 0, RepeatedlyReceiveSignal, 1).Get(ctx, &response)
	if err != nil {
		return "", err
	}
	return *response.RunID, nil
}

func RepeatedlyReceiveSignal(ctx workflow.Context, count int) ([]int, error) {
	var signals []int
	ch := workflow.GetSignalChannel(ctx, "signal")
	for range count {
		var signal int
		if !ch.Receive(ctx, &signal) {
			return nil, ctx.Err()
		}
		signals = append(signals, signal)
	}
	return signals, nil
}

// -----------------------------------------------------------------------------
// TESTS
// -----------------------------------------------------------------------------

// TestWorkflowSignalWithStart executes [RepeatedlySignalAndStart], which uses
// signal-with-start to signal [RepeatedlyReceiveSignal] several times.
// We expect the signalled workflow to start automatically and return
// the signals back to us.
func (ts *SystemNexusTestSuite) TestWorkflowSignalWithStart() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	id := uuid.NewString()
	callerID := ts.T().Name() + "-caller-" + id
	calleeID := ts.T().Name() + "-callee-" + id
	numCalls := 5

	// The responses from each call are written into the responses array.
	var responses []workflow.SignalWithStartWorkflowResponse
	ts.NoError(ts.executeWorkflow(executeWorkflowParams{
		ID:       callerID,
		Workflow: RepeatedlySignalAndStart,
		Args:     []any{calleeID, numCalls},
		Result:   &responses,
	}))

	// Expect 5 responses, all with the same ID. The first response also started the workflow.
	ts.Len(responses, numCalls)
	for i := range responses {
		if i == 0 {
			ts.True(*responses[0].Started)
			continue
		}
		ts.False(*responses[i].Started)
		ts.Equal(*responses[0].RunID, *responses[i].RunID)
	}

	// The callee returns an array with all its signals.
	var wantSignals, gotSignals []int
	ts.NoError(ts.client.GetWorkflow(ctx, calleeID, *responses[0].RunID).Get(ctx, &gotSignals))
	for i := range responses {
		wantSignals = append(wantSignals, i)
	}
	ts.Equal(wantSignals, gotSignals)
}

// TestWorkflowSignalWithStartWithLotsOfOptions invokes [SignalAndStartWithLotsOfOptions],
// which exercises all the options except cron, which is incompatible with start delay.
func (ts *SystemNexusTestSuite) TestWorkflowSignalWithStartWithLotsOfOptions() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	id := uuid.NewString()
	callerID := ts.T().Name() + "-caller-" + id
	calleeID := ts.T().Name() + "-callee-" + id
	options := LotsOfOptions{
		ID:                       calleeID,
		TaskQueue:                ts.taskQueueName,
		WorkflowExecutionTimeout: 20 * time.Second,
		WorkflowRunTimeout:       15 * time.Second,
		WorkflowTaskTimeout:      5 * time.Second,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     3,
			MaximumInterval:        5 * time.Second,
			MaximumAttempts:        2,
			NonRetryableErrorTypes: []string{"NonRetryableError"},
		},
		Memo:                   map[string]any{"memo": "memo value"},
		KeywordSearchField:     "CustomKeywordField",
		KeywordSearchAttribute: "keyword",
		Priority:               &temporal.Priority{PriorityKey: 2, FairnessKey: "tenant", FairnessWeight: 2},
		VersioningOverride:     client.AutoUpgradeVersioningOverride{},
		StartDelay:             time.Second,
		StaticSummary:          "summary",
		StaticDetails:          "details",
	}

	var calleeRunID string
	ts.NoError(ts.executeWorkflow(executeWorkflowParams{
		ID:       callerID,
		Workflow: SignalAndStartWithLotsOfOptions,
		Args:     []any{options},
		Result:   &calleeRunID,
	}))
	ts.NoError(ts.client.GetWorkflow(ctx, calleeID, calleeRunID).Get(ctx, nil))

	request := ts.signalWithStartRequestFromHistory(ctx, callerID)
	ts.Equal(options.ID, request.GetWorkflowId())
	ts.Equal(options.TaskQueue, request.GetTaskQueue().GetName())
	ts.Equal(options.WorkflowIDReusePolicy, request.GetWorkflowIdReusePolicy())
	ts.Equal(options.WorkflowIDConflictPolicy, request.GetWorkflowIdConflictPolicy())
	ts.Equal(options.WorkflowExecutionTimeout, request.GetWorkflowExecutionTimeout().AsDuration())
	ts.Equal(options.WorkflowRunTimeout, request.GetWorkflowRunTimeout().AsDuration())
	ts.Equal(options.WorkflowTaskTimeout, request.GetWorkflowTaskTimeout().AsDuration())
	ts.Equal(options.StartDelay, request.GetWorkflowStartDelay().AsDuration())
	ts.Equal(options.RetryPolicy.InitialInterval, request.GetRetryPolicy().GetInitialInterval().AsDuration())
	ts.Equal(options.RetryPolicy.BackoffCoefficient, request.GetRetryPolicy().GetBackoffCoefficient())
	ts.Equal(options.RetryPolicy.MaximumInterval, request.GetRetryPolicy().GetMaximumInterval().AsDuration())
	ts.Equal(options.RetryPolicy.MaximumAttempts, request.GetRetryPolicy().GetMaximumAttempts())
	ts.Equal(options.RetryPolicy.NonRetryableErrorTypes, request.GetRetryPolicy().GetNonRetryableErrorTypes())
	ts.Equal(int32(options.Priority.PriorityKey), request.GetPriority().GetPriorityKey())
	ts.Equal(options.Priority.FairnessKey, request.GetPriority().GetFairnessKey())
	ts.Equal(options.Priority.FairnessWeight, request.GetPriority().GetFairnessWeight())
	ts.NotNil(request.GetVersioningOverride().GetAutoUpgrade())

	dc := converter.WithDataConverterSerializationContext(
		converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &intTestSigningCodec{}),
		converter.WorkflowSerializationContext{Namespace: ts.config.Namespace, WorkflowID: calleeID},
	)
	var memo, keyword, summary, details string
	ts.NoError(dc.FromPayload(request.GetMemo().GetFields()["memo"], &memo))
	ts.Equal(options.Memo["memo"], memo)
	ts.NoError(converter.GetDefaultDataConverter().FromPayload(
		request.GetSearchAttributes().GetIndexedFields()[options.KeywordSearchField], &keyword,
	))
	ts.Equal(options.KeywordSearchAttribute, keyword)
	ts.NoError(dc.FromPayload(request.GetUserMetadata().GetSummary(), &summary))
	ts.Equal(options.StaticSummary, summary)
	ts.NoError(dc.FromPayload(request.GetUserMetadata().GetDetails(), &details))
	ts.Equal(options.StaticDetails, details)
}

// TestWorkflowSignalWithStartWithCron invokes [SignalAndStartWithCron].
// It has a special test because cron and start delay are incompatible.
func (ts *SystemNexusTestSuite) TestWorkflowSignalWithStartWithCron() {
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()

	id := uuid.NewString()
	callerID := ts.T().Name() + "-caller-" + id
	calleeID := ts.T().Name() + "-callee-" + id

	ts.NoError(ts.executeWorkflow(executeWorkflowParams{
		ID:       callerID,
		Workflow: SignalAndStartWithCron,
		Args:     []any{calleeID},
	}))
	defer func() {
		ts.NoError(ts.client.TerminateWorkflow(ctx, calleeID, "", "test cleanup"))
	}()

	request := ts.signalWithStartRequestFromHistory(ctx, callerID)
	ts.Equal("@every 1s", request.GetCronSchedule())
	ts.Nil(request.GetWorkflowStartDelay())
}
