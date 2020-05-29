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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	filterpb "go.temporal.io/temporal-proto/filter"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.uber.org/zap"

	"go.temporal.io/temporal/client"
	"go.temporal.io/temporal/worker"
	"go.temporal.io/temporal/workflow"
)

type AsyncBindingsTestSuite struct {
	*require.Assertions
	suite.Suite
	config       Config
	client       client.Client
	worker       worker.Worker
	taskListName string
	seq          int64
}

func SimplestWorkflow(ctx workflow.Context) error {
	return nil
}

func TestAsyncBindingsTestSuite(t *testing.T) {
	suite.Run(t, new(AsyncBindingsTestSuite))
}

func (ts *AsyncBindingsTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.config = NewConfig()
	ts.NoError(WaitForTCP(time.Minute, ts.config.ServiceAddr))
	logger, err := zap.NewDevelopment()
	ts.NoError(err)
	ts.client, err = client.NewClient(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: namespace,
		Logger:    logger,
	})
	ts.NoError(err)
	ts.registerNamespace()
}

func (ts *AsyncBindingsTestSuite) registerNamespace() {
	client, err := client.NewNamespaceClient(client.Options{HostPort: ts.config.ServiceAddr})
	ts.NoError(err)
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	name := namespace
	retention := int32(1)
	err = client.Register(ctx, &workflowservice.RegisterNamespaceRequest{
		Name:                                   name,
		WorkflowExecutionRetentionPeriodInDays: retention,
	})
	defer client.Close()
	if _, ok := err.(*serviceerror.NamespaceAlreadyExists); ok {
		return
	}
	ts.NoError(err)
	time.Sleep(namespaceCacheRefreshInterval) // wait for namespace cache refresh on temporal-server
	// bellow is used to guarantee namespace is ready
	var dummyReturn string
	err = ts.executeWorkflow("test-namespace-exist", SimplestWorkflow, &dummyReturn)
	numOfRetry := 20
	for err != nil && numOfRetry >= 0 {
		if _, ok := err.(*serviceerror.NotFound); ok {
			time.Sleep(namespaceCacheRefreshInterval)
			err = ts.executeWorkflow("test-namespace-exist", SimplestWorkflow, &dummyReturn)
		} else {
			break
		}
		numOfRetry--
	}
}

// executeWorkflow executes a given workflow and waits for the result
func (ts *AsyncBindingsTestSuite) executeWorkflow(
	wfID string, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	options := ts.startWorkflowOptions(wfID)
	return ts.executeWorkflowWithOption(options, wfFunc, retValPtr, args...)
}

func (ts *AsyncBindingsTestSuite) executeWorkflowWithOption(
	options client.StartWorkflowOptions, wfFunc interface{}, retValPtr interface{}, args ...interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	run, err := ts.client.ExecuteWorkflow(ctx, options, wfFunc, args...)
	if err != nil {
		return err
	}
	err = run.Get(ctx, retValPtr)
	if ts.config.Debug {
		iter := ts.client.GetWorkflowHistory(ctx, options.ID, run.GetRunID(), false, filterpb.HistoryEventFilterType_AllEvent)
		for iter.HasNext() {
			event, err1 := iter.Next()
			if err1 != nil {
				break
			}
			fmt.Println(event.String())
		}
	}
	return err
}

func (ts *AsyncBindingsTestSuite) startWorkflowOptions(wfID string) client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                       wfID,
		TaskList:                 ts.taskListName,
		WorkflowExecutionTimeout: 15 * time.Second,
		WorkflowTaskTimeout:      time.Second,
		WorkflowIDReusePolicy:    client.WorkflowIDReusePolicyAllowDuplicate,
	}
}

func (ts *AsyncBindingsTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *AsyncBindingsTestSuite) SetupTest() {
	ts.seq++
	ts.taskListName = fmt.Sprintf("tl-%v-%s", ts.seq, ts.T().Name())
	options := worker.Options{
		DisableStickyExecution: ts.config.IsStickyOff,
	}
	ts.worker = worker.New(ts.client, ts.taskListName, options)
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
	wr, err := ts.client.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{TaskList: ts.taskListName}, name)
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
	ts.worker.RegisterActivity(SingleActivity)
	ts.NoError(ts.worker.Start())
	wr, err := ts.client.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{TaskList: ts.taskListName}, name)
	ts.NoError(err)
	err = ts.client.SignalWorkflow(context.Background(), wr.GetID(), wr.GetRunID(), "signalFoo", "!!")
	ts.NoError(err)
	var result string
	ts.NoError(wr.Get(context.Background(), &result))
	ts.Equal("Hello World!!!", result)
}
