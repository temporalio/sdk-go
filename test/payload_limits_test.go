package test_test

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type PayloadLimitsTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	server *testsuite.DevServer
	worker worker.Worker
}

func TestPayloadLimitsTestSuite(t *testing.T) {
	suite.Run(t, new(PayloadLimitsTestSuite))
}

const PAYLOAD_SIZE_ERROR_LIMIT = 10 * 1024 // 10 KiB

func (ts *PayloadLimitsTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.config = NewConfig(
		WithNamespace("payload-limits-namespace"),
		WithServiceAddr("127.0.0.1:7234"),
		WithServiceHTTPAddr("127.0.0.1:7244"),
	)

	_, httpPort, err := net.SplitHostPort(ts.config.ServiceHTTPAddr)
	ts.NoError(err)

	// Start dev server with low payload limits
	ts.server, err = testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{
			Version: "v1.6.0",
		},
		ClientOptions: &client.Options{
			HostPort:  ts.config.ServiceAddr,
			Namespace: ts.config.Namespace,
		},
		LogLevel: "warn",
		ExtraArgs: []string{
			"--http-port", httpPort,
			"--dynamic-config-value", fmt.Sprintf("limit.blobSize.error=%d", PAYLOAD_SIZE_ERROR_LIMIT),
			"--dynamic-config-value", "limit.blobSize.warn=2048", // 2 KiB
		},
	})
	ts.NoError(err)

	ts.NoError(WaitForTCP(time.Minute, ts.config.ServiceAddr))
}

func (ts *PayloadLimitsTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	// Only initialize if not already set (tests can call ResetClientAndWorker themselves)
	if ts.client == nil {
		ts.NoError(ts.InitClient())
		ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
		ts.NoError(ts.worker.Start())
	}
}

func (ts *PayloadLimitsTestSuite) TearDownTest() {
	if ts.worker != nil {
		ts.worker.Stop()
		ts.worker = nil
	}
	if ts.client != nil {
		ts.client.Close()
		ts.client = nil
	}
}

func (ts *PayloadLimitsTestSuite) TearDownSuite() {
	ts.server.Stop()
}

func (ts *PayloadLimitsTestSuite) ResetClientAndWorker(
	clientOpt ConfigureClientOptions,
	workerOpt ConfigureWorkerOptions) {
	if ts.worker != nil {
		ts.worker.Stop()
		ts.worker = nil
	}
	if ts.client != nil {
		ts.client.Close()
		ts.client = nil
	}
	if clientOpt == nil {
		ts.NoError(ts.InitClient())
	} else {
		ts.NoError(ts.InitClient(clientOpt))
	}
	workerOptions := worker.Options{}
	if workerOpt != nil {
		workerOpt(&workerOptions)
	}
	ts.worker = worker.New(ts.client, ts.taskQueueName, workerOptions)
	ts.NoError(ts.worker.Start())
}

// pollForHistoryEvent polls the workflow history until an event of the specified type is found.
// Returns the last event of the specified type, or nil if not found within the polling period.
func (ts *PayloadLimitsTestSuite) pollForHistoryEvent(
	ctx context.Context,
	workflowID string,
	runID string,
	eventType enumspb.EventType,
) *historypb.HistoryEvent {
	var lastEvent *historypb.HistoryEvent
	for range 100 {
		time.Sleep(100 * time.Millisecond)
		eventIterator := ts.client.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		for eventIterator.HasNext() {
			event, err := eventIterator.Next()
			ts.NoError(err)
			if event.EventType == eventType {
				lastEvent = event
			}
		}
		if lastEvent != nil {
			break
		}
	}
	return lastEvent
}

// assertWorkflowTaskFailedWithPayloadLimit verifies that the event is a WORKFLOW_TASK_FAILED
// event with the expected payload limit failure attributes.
func (ts *PayloadLimitsTestSuite) assertWorkflowTaskFailedWithPayloadLimit(event *historypb.HistoryEvent) {
	ts.NotNil(event)
	attributes := event.GetWorkflowTaskFailedEventAttributes()
	ts.NotNil(attributes)
	ts.Equal(enumspb.WORKFLOW_TASK_FAILED_CAUSE_PAYLOADS_TOO_LARGE, attributes.Cause)
	ts.NotNil(attributes.Failure)
	ts.Equal("[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.", attributes.Failure.Message)
}

// assertLogContains verifies that the logger contains a line with the specified message.
func (ts *PayloadLimitsTestSuite) assertLogContains(logger *ilog.MemoryLogger, message string) {
	ts.True(slices.ContainsFunc(logger.Lines(), func(line string) bool {
		return strings.Contains(line, message)
	}))
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorWorkflowResult() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	wfname := "payload-size-error-workflow-result"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) (string, error) {
			return strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000), nil
		},
		workflow.RegisterOptions{Name: wfname},
	)
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfname,
	)
	ts.NoError(err)

	lastWorkflowTaskFailedEvent := ts.pollForHistoryEvent(ctx, run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED)

	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	ts.assertWorkflowTaskFailedWithPayloadLimit(lastWorkflowTaskFailedEvent)

	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorUpdateResult() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	wfname := "payload-size-error-update-result"
	updateName := "large-update"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) error {
			err := workflow.SetUpdateHandler(ctx, updateName, func(ctx workflow.Context) (string, error) {
				return strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000), nil
			})
			if err != nil {
				return err
			}
			workflow.GetSignalChannel(ctx, "finish").Receive(ctx, nil)
			return nil
		},
		workflow.RegisterOptions{Name: wfname},
	)

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfname,
	)
	ts.NoError(err)

	go func() {
		// UpdateWorkflow blocks until the update is accepted, which never happens
		// because the update handler doesn't complete. Run in a goroutine and monitor
		// event history.
		ts.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
			WorkflowID:   run.GetID(),
			RunID:        run.GetRunID(),
			UpdateName:   updateName,
			WaitForStage: client.WorkflowUpdateStageAccepted,
		})
	}()

	lastWorkflowTaskFailedEvent := ts.pollForHistoryEvent(ctx, run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED)

	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	ts.assertWorkflowTaskFailedWithPayloadLimit(lastWorkflowTaskFailedEvent)

	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorChildWorkflowInput() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	childWfName := "child-workflow"
	parentWfName := "payload-size-error-child-workflow-input"

	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input string) (string, error) {
			return input, nil
		},
		workflow.RegisterOptions{Name: childWfName},
	)
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) error {
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowExecutionTimeout: 10 * time.Second,
			})
			// Try to start child workflow with input that exceeds payload limit
			largeInput := strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000)
			return workflow.ExecuteChildWorkflow(childCtx, childWfName, largeInput).Get(ctx, nil)
		},
		workflow.RegisterOptions{Name: parentWfName},
	)

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		parentWfName,
	)
	ts.NoError(err)

	lastWorkflowTaskFailedEvent := ts.pollForHistoryEvent(ctx, run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED)

	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	ts.assertWorkflowTaskFailedWithPayloadLimit(lastWorkflowTaskFailedEvent)

	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorChildWorkflowMemo() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	childWfName := "child-workflow-with-memo"
	parentWfName := "payload-size-error-child-workflow-memo"

	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) error {
			return nil
		},
		workflow.RegisterOptions{Name: childWfName},
	)
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) error {
			largeMemoValue := strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000)
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowExecutionTimeout: 10 * time.Second,
				Memo: map[string]interface{}{
					"large-field": largeMemoValue,
				},
			})
			return workflow.ExecuteChildWorkflow(childCtx, childWfName).Get(ctx, nil)
		},
		workflow.RegisterOptions{Name: parentWfName},
	)

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		parentWfName,
	)
	ts.NoError(err)

	lastWorkflowTaskFailedEvent := ts.pollForHistoryEvent(ctx, run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED)

	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	ts.assertWorkflowTaskFailedWithPayloadLimit(lastWorkflowTaskFailedEvent)

	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorActivityInput() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	wfName := "payload-size-error-activity-result"
	actName := "large-payload-activity"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) (s string, err error) {
			err = workflow.ExecuteActivity(
				workflow.WithActivityOptions(
					ctx,
					workflow.ActivityOptions{ScheduleToCloseTimeout: 5 * time.Second},
				),
				actName,
				strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000),
			).Get(ctx, &s)
			return
		},
		workflow.RegisterOptions{Name: wfName},
	)
	ts.worker.RegisterActivityWithOptions(
		func(ctx context.Context, input string) (string, error) { return input, nil },
		activity.RegisterOptions{Name: actName},
	)
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfName,
	)
	ts.NoError(err)

	lastWorkflowTaskFailedEvent := ts.pollForHistoryEvent(ctx, run.GetID(), run.GetRunID(), enumspb.EVENT_TYPE_WORKFLOW_TASK_FAILED)

	ts.NoError(ts.client.CancelWorkflow(ctx, run.GetID(), run.GetRunID()))

	ts.assertWorkflowTaskFailedWithPayloadLimit(lastWorkflowTaskFailedEvent)

	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorActivityResult() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
	}, nil)

	wfName := "payload-size-error-activity-result"
	actName := "large-payload-activity"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) (s string, err error) {
			err = workflow.ExecuteActivity(
				workflow.WithActivityOptions(
					ctx,
					workflow.ActivityOptions{ScheduleToCloseTimeout: 5 * time.Second},
				),
				actName,
			).Get(ctx, &s)
			return
		},
		workflow.RegisterOptions{Name: wfName},
	)
	ts.worker.RegisterActivityWithOptions(
		func(context.Context) (string, error) { return strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000), nil },
		activity.RegisterOptions{Name: actName},
	)
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfName,
	)
	ts.NoError(err)

	// Activity task failed events are not in history unless the workflow is closed.
	// Wait for the activity to timeout and the workflow to close before examining
	// the result and history.
	var res string
	err = run.Get(ctx, &res)
	var workflowExecutionErr *temporal.WorkflowExecutionError
	ts.ErrorAs(err, &workflowExecutionErr)
	var activityErr *temporal.ActivityError
	ts.ErrorAs(workflowExecutionErr.Unwrap(), &activityErr)
	var applicationErr *temporal.ApplicationError
	ts.ErrorAs(activityErr.Unwrap(), &applicationErr)

	eventIterator := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var lastActivityTaskFailedEvent *historypb.HistoryEvent
	for eventIterator.HasNext() {
		event, err := eventIterator.Next()
		ts.NoError(err)
		if event.EventType == enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED {
			lastActivityTaskFailedEvent = event
		}
	}
	ts.NotNil(lastActivityTaskFailedEvent)
	attributes := lastActivityTaskFailedEvent.GetActivityTaskFailedEventAttributes()
	ts.NotNil(attributes)
	ts.NotNil(attributes.Failure)
	ts.Equal("[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.", attributes.Failure.Message)

	// Verify failure is logged
	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the error limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeErrorDisabledWorkflowResult() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(
		func(opts *client.Options) {
			opts.Logger = logger
		},
		func(opts *worker.Options) {
			opts.DisablePayloadErrorLimit = true
		})

	wfname := "payload-size-error-workflow-result"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) (string, error) {
			return strings.Repeat("a", PAYLOAD_SIZE_ERROR_LIMIT+1000), nil
		},
		workflow.RegisterOptions{Name: wfname},
	)
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfname,
	)
	ts.NoError(err)

	var res string
	var workflowExecutionErr *temporal.WorkflowExecutionError
	ts.ErrorAs(run.Get(ctx, &res), &workflowExecutionErr)
	var terminatedErr *temporal.TerminatedError
	ts.ErrorAs(workflowExecutionErr.Unwrap(), &terminatedErr)

	eventIterator := ts.client.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var workflowExecutionTerminatedEvent *historypb.HistoryEvent
	for eventIterator.HasNext() {
		event, err := eventIterator.Next()
		ts.NoError(err)
		if event.EventType == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED {
			workflowExecutionTerminatedEvent = event
		}
	}
	ts.NotNil(workflowExecutionTerminatedEvent)
	attributes := workflowExecutionTerminatedEvent.GetWorkflowExecutionTerminatedEventAttributes()
	ts.NotNil(attributes)
	ts.Contains(attributes.Reason, "BadScheduleActivityAttributes: CompleteWorkflowExecutionCommandAttributes.Result exceeds size limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeWarningClientCustom() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
		opts.PayloadLimits = client.PayloadLimitOptions{
			PayloadSizeWarning: 512,
		}
	}, nil)

	wfname := "payload-size-warning-workflow-result"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input string) (int, error) { return len(input), nil },
		workflow.RegisterOptions{Name: wfname},
	)

	input := strings.Repeat("i", 1024)
	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfname,
		input,
	)
	ts.NoError(err)

	var res int
	ts.NoError(run.Get(ctx, &res))
	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
}

func (ts *PayloadLimitsTestSuite) TestPayloadSizeWarningWorkflowCustom() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := ilog.NewMemoryLogger()
	ts.ResetClientAndWorker(func(opts *client.Options) {
		opts.Logger = logger
		opts.PayloadLimits = client.PayloadLimitOptions{
			PayloadSizeWarning: 512,
		}
	}, nil)

	wfName := "payload-size-warning-activity-result"
	actName := "large-payload-activity"
	ts.worker.RegisterWorkflowWithOptions(
		func(ctx workflow.Context) (s string, err error) {
			err = workflow.ExecuteActivity(
				workflow.WithActivityOptions(
					ctx,
					workflow.ActivityOptions{ScheduleToCloseTimeout: 10 * time.Second},
				),
				actName,
			).Get(ctx, &s)
			return
		},
		workflow.RegisterOptions{Name: wfName},
	)
	ts.worker.RegisterActivityWithOptions(
		func(context.Context) (string, error) { return strings.Repeat("a", 1024), nil },
		activity.RegisterOptions{Name: actName},
	)

	run, err := ts.client.ExecuteWorkflow(
		ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		wfName,
	)
	ts.NoError(err)

	var res string
	ts.NoError(run.Get(ctx, &res))
	ts.assertLogContains(logger, "[TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
}
