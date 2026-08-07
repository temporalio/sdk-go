package opentelemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func nestedSpansMultipleTracersWorkflow(ctx workflow.Context) error {
	tracerA := Tracer("a")
	tracerB := Tracer("b")

	outerCtx, outer := tracerA.Start(ctx, "outer")
	innerCtx, inner := tracerB.Start(outerCtx, "inner")
	_, leaf := tracerA.Start(innerCtx, "leaf")

	leaf.End()
	inner.End()
	outer.End()
	return nil
}

func spanInQueryHandlerWorkflowMultipleTracers(ctx workflow.Context) error {
	tracer := Tracer("a")

	rootCtx, root := tracer.Start(ctx, "root")
	defer root.End()

	if err := workflow.SetQueryHandler(rootCtx, "testQuery", func() (string, error) {
		tracer := Tracer("b")
		_, span := tracer.Start(rootCtx, "query-span")
		span.End()
		return "ok", nil
	}); err != nil {
		return err
	}

	workflow.GetSignalChannel(rootCtx, "finish").Receive(rootCtx, nil)
	return nil
}

func spanInUpdateValidatorWorkflowMultipleTracers(ctx workflow.Context) error {
	tracer := Tracer("a")

	rootCtx, root := tracer.Start(ctx, "root")
	defer root.End()

	if err := workflow.SetUpdateHandlerWithOptions(rootCtx, "testUpdate",
		func(ctx workflow.Context) error { return nil },
		workflow.UpdateHandlerOptions{
			Validator: func(ctx workflow.Context) error {
				tracer := Tracer("b")
				_, span := tracer.Start(ctx, "validate-span")
				span.End()
				return nil
			},
		}); err != nil {
		return err
	}

	workflow.GetSignalChannel(rootCtx, "finish").Receive(rootCtx, nil)
	return nil
}

type tracerTestSuite struct {
	otelTestSuite
}

func TestTracerTestSuite(t *testing.T) {
	suite.Run(t, new(tracerTestSuite))
}

func (s *tracerTestSuite) TestTracerProviderSpanTree() {
	queryHandler := func(env *testsuite.TestWorkflowEnvironment) {
		_, err := env.QueryWorkflow("testQuery")
		s.Require().NoError(err)
		env.SignalWorkflow("finish", nil)
	}
	updateValidator := func(env *testsuite.TestWorkflowEnvironment) {
		env.UpdateWorkflow("testUpdate", "updateID", &testsuite.TestUpdateCallback{})
		env.SignalWorkflow("finish", nil)
	}

	for _, test := range []struct {
		operation string
		workflow  func(workflow.Context) error
		callback  func(*testsuite.TestWorkflowEnvironment)
		spanTree  []string
	}{
		{
			operation: "workflow task",
			workflow:  nestedSpansMultipleTracersWorkflow,
			spanTree: []string{
				"RunWorkflow:nestedSpansMultipleTracersWorkflow",
				"  outer",
				"    inner",
				"      leaf",
			},
		},
		{
			operation: "query handler",
			workflow:  spanInQueryHandlerWorkflowMultipleTracers,
			callback:  queryHandler,
			spanTree: []string{
				"HandleQuery:testQuery",
				"HandleSignal:finish",
				"RunWorkflow:spanInQueryHandlerWorkflowMultipleTracers",
				"  root",
				"    query-span",
			},
		},
		{
			operation: "update validator",
			workflow:  spanInUpdateValidatorWorkflowMultipleTracers,
			callback:  updateValidator,
			spanTree: []string{
				"ValidateUpdate:testUpdate",
				"  validate-span",
				"HandleUpdate:testUpdate",
				"HandleSignal:finish",
				"RunWorkflow:spanInUpdateValidatorWorkflowMultipleTracers",
				"  root",
			},
		},
	} {
		s.Run(test.operation, func() {
			recorder, env := s.newTestWorkflowEnvironment()
			env.RegisterWorkflow(test.workflow)
			if test.callback != nil {
				env.RegisterDelayedCallback(func() { test.callback(env) }, time.Millisecond)
			}

			env.ExecuteWorkflow(test.workflow)
			s.Require().NoError(env.GetWorkflowError())
			s.Require().Equal(test.spanTree, formatSpanTree(recorder.Ended()))
		})
	}
}
