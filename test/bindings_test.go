// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
	ts.NoError(ts.InitConfigAndClient())
}

func (ts *AsyncBindingsTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *AsyncBindingsTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
	options := worker.Options{
		DisableStickyExecution: ts.config.maxWorkflowCacheSize <= 0,
	}
	ts.worker = worker.New(ts.client, ts.taskQueueName, options)
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
