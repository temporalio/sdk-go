package opentelemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

type tracerIntegrationTestSuite struct {
	otelTestSuite
	client    client.Client
	worker    worker.Worker
	taskQueue string
	recorder  *tracetest.SpanRecorder
}

func TestTracerIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(tracerIntegrationTestSuite))
}

func (s *tracerIntegrationTestSuite) SetupSuite() {
	s.otelTestSuite.SetupSuite()
	s.recorder = s.newSpanRecorder()
	plugin, err := NewPlugin(PluginOptions{})
	s.Require().NoError(err)
	s.client = s.newDevServerClient(
		client.Options{Plugins: []client.Plugin{plugin}},
		testsuite.DevServerOptions{},
	)
}

func (s *tracerIntegrationTestSuite) SetupTest() {
	s.recorder.Reset()
	s.taskQueue = "opentelemetry-v2-tracer-" + uuid.NewString()
	s.worker = worker.New(s.client, s.taskQueue, worker.Options{})
	s.worker.RegisterWorkflow(tracerWorkflow)
	s.worker.RegisterWorkflow(tracerResetWorkflow)
	s.worker.RegisterWorkflow(tracerResetLateSourceWorkflow)
	s.worker.RegisterWorkflow(tracerResetDuringSpan)
	s.worker.RegisterWorkflow(tracerWorkflowTaskRetry)
	s.worker.RegisterWorkflow(chainedContinueAsNewWorkflow)
	s.Require().NoError(s.worker.Start())
}

func (s *tracerIntegrationTestSuite) TearDownTest() {
	s.worker.Stop()
}

func (s *tracerIntegrationTestSuite) TestTracerWorkflow() {
	ctx := context.Background()

	run, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: s.taskQueue,
	}, tracerWorkflow, false)
	s.Require().NoError(err)

	_, err = s.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   run.GetID(),
		UpdateName:   tracerTestUpdateName,
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	s.Require().NoError(err)

	_, err = s.client.QueryWorkflow(ctx, run.GetID(), "", tracerTestQueryName)
	s.Require().NoError(err)

	s.Require().NoError(s.client.SignalWorkflow(ctx, run.GetID(), "", tracerTestSignalName, nil))

	s.Require().NoError(run.Get(ctx, nil))

	_, err = s.client.QueryWorkflow(ctx, run.GetID(), "", tracerTestQueryName)
	s.Require().NoError(err)

	spans := s.recorder.Ended()

	s.Require().Equal([]string{
		"validate start",
		"update start",
		"query start",
		"process start",
		"  record results",
		"process start", // ContinueAsNew
		"  record results",
		"    query start",
	}, s.formatSpanTree(spans))
	s.requireUniqueSpanIDs(spans)
}

func (s *tracerIntegrationTestSuite) TestContinueAsNewUnderUserSpan() {
	ctx := context.Background()

	run, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: s.taskQueue,
	}, chainedContinueAsNewWorkflow, false)
	s.Require().NoError(err)
	s.Require().NoError(run.Get(ctx, nil))

	spans := s.recorder.Ended()

	s.Require().Equal([]string{
		"chained-span",
		"  chained-span",
	}, s.formatSpanTree(spans))
	s.requireUniqueSpanIDs(spans)
}

func (s *tracerIntegrationTestSuite) TestWorkflowTaskRetryReusesSpanID() {
	ctx := context.Background()
	run, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:          s.taskQueue,
		WorkflowRunTimeout: time.Second,
		RetryPolicy:        &temporal.RetryPolicy{MaximumAttempts: 1},
	}, tracerWorkflowTaskRetry)
	s.Require().NoError(err)
	err = run.Get(ctx, nil)
	s.Require().True(temporal.IsTimeoutError(err))

	spans := s.recorder.Ended()
	s.Require().Greater(len(spans), 1)

	spanIDs := make(map[trace.SpanID]bool, len(spans))
	for _, span := range spans {
		spanIDs[span.SpanContext().SpanID()] = true
	}
	s.Require().Len(spanIDs, 1)
}

func (s *tracerIntegrationTestSuite) TestTracerReset() {
	tests := []struct {
		name          string
		workflow      interface{}
		spanTree      []string
		verifySpanIDs func([]sdktrace.ReadOnlySpan)
	}{
		{
			name:     "tracer created before reset point",
			workflow: tracerResetWorkflow,
			spanTree: []string{
				"process start",
				"  record results",
				"  record results",
			},
			verifySpanIDs: s.requireUniqueSpanIDs,
		},
		{
			name:     "tracer created after reset point",
			workflow: tracerResetLateSourceWorkflow,
			spanTree: []string{
				"process start",
				"  record results",
				"  record results",
			},
			verifySpanIDs: s.requireUniqueSpanIDs,
		},
		{
			name:     "span crosses reset point",
			workflow: tracerResetDuringSpan,
			spanTree: []string{
				"process start",
				"  record results",
				"process start",
				"  record results",
			},
			verifySpanIDs: func(spans []sdktrace.ReadOnlySpan) {
				// These spans reuse their IDs because they were created before the reset point.
				s.Require().Equal(spans[0].SpanContext(), spans[2].SpanContext())
				s.Require().Equal(spans[1].SpanContext(), spans[3].SpanContext())

				// Verify the new spans are created after the old ones.
				s.Require().True(spans[2].EndTime().After(spans[0].EndTime()))
				s.Require().True(spans[3].EndTime().After(spans[1].EndTime()))
			},
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			s.recorder.Reset()
			ctx := context.Background()

			run, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
				TaskQueue: s.taskQueue,
			}, test.workflow)
			s.Require().NoError(err)

			_, err = s.client.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), client.QueryTypeStackTrace)
			s.Require().NoError(err)
			s.Require().NoError(s.client.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), tracerTestSignalName, nil))
			s.Require().NoError(run.Get(ctx, nil))

			reset, err := s.client.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
				Namespace: client.DefaultNamespace,
				WorkflowExecution: &commonpb.WorkflowExecution{
					WorkflowId: run.GetID(),
					RunId:      run.GetRunID(),
				},
				WorkflowTaskFinishEventId: 8,
			})
			s.Require().NoError(err)
			s.Require().NoError(s.client.GetWorkflow(ctx, run.GetID(), reset.GetRunId()).Get(ctx, nil))

			spans := s.recorder.Ended()
			s.Require().Equal(test.spanTree, s.formatSpanTree(spans))
			test.verifySpanIDs(spans)
		})
	}
}
