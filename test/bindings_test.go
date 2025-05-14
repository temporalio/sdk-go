package test_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type AsyncBindingsTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker        worker.Worker
	taskQueueName string
}

func TestAsyncBindingsTestSuite(t *testing.T) {
	suite.Run(t, new(AsyncBindingsTestSuite))
}

func (ts *AsyncBindingsTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *AsyncBindingsTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *AsyncBindingsTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(SimplestWorkflow)
}

func (ts *AsyncBindingsTestSuite) TearDownTest() {
	ts.worker.Stop()
}

func (ts *AsyncBindingsTestSuite) TestEmptyWorkflowDefinition() {
	name := "empty"
	ts.worker.RegisterWorkflowWithOptions(
		&EmptyWorkflowDefinitionFactory{},
		workflow.RegisterOptions{Name: name},
	)
	ts.NoError(ts.worker.Start())
	wr, err := ts.client.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{TaskQueue: ts.taskQueueName}, name)
	ts.NoError(err)
	var result string
	ts.NoError(wr.Get(context.Background(), &result))
	ts.Equal("EmptyResult", result)
}

func (ts *AsyncBindingsTestSuite) TestSingleActivityWorkflowDefinition() {
	name := "singleActivity"
	ts.worker.RegisterWorkflowWithOptions(
		&SingleActivityWorkflowDefinitionFactory{},
		workflow.RegisterOptions{Name: name},
	)
	ts.worker.RegisterWorkflow(ChildWorkflow)
	ts.worker.RegisterActivity(Activity1)
	ts.worker.RegisterActivity(ActivityThatFails)
	ts.NoError(ts.worker.Start())
	wr, err := ts.client.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{TaskQueue: ts.taskQueueName}, name)
	ts.NoError(err)
	err = ts.client.SignalWorkflow(context.Background(), wr.GetID(), wr.GetRunID(), "signalFoo", "!!")
	ts.NoError(err)
	var result string
	ts.NoError(wr.Get(context.Background(), &result))
	ts.Equal("Hello World!!!", result)
}
