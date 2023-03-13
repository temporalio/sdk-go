// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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
)

type WorkerVersioningTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows *Workflows
}

func TestWorkerVersioningTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerVersioningTestSuite))
}

func (ts *WorkerVersioningTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerVersioningTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerVersioningTestSuite) SetupTest() {
	ts.T().Skip("Skipped until server is updated and this works")
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerVersioningTestSuite) TestManipulateVersionGraph() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	err := ts.client.UpdateWorkerBuildIDCompatability(ctx, &client.UpdateWorkerBuildIDCompatabilityOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "1.0",
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIDCompatability(ctx, &client.UpdateWorkerBuildIDCompatabilityOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "2.0",
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIDCompatability(ctx, &client.UpdateWorkerBuildIDCompatabilityOptions{
		TaskQueue:         ts.taskQueueName,
		WorkerBuildID:     "1.1",
		CompatibleBuildID: "1.0",
	})
	ts.NoError(err)

	res, err := ts.client.GetWorkerBuildIDCompatability(ctx, &client.GetWorkerBuildIDCompatabilityOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)
	ts.Equal("2.0", res.Default())
	ts.Equal("1.1", res.Sets[0].BuildIDs[1])
	ts.Equal("1.0", res.Sets[0].BuildIDs[0])
}

func (ts *WorkerVersioningTestSuite) TestTwoWorkersGetDifferentTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	err := ts.client.UpdateWorkerBuildIDCompatability(ctx, &client.UpdateWorkerBuildIDCompatabilityOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "1.0",
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildIDForVersioning: "1.0"})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()
	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildIDForVersioning: "2.0"})
	ts.workflows.register(worker2)
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// Start some workflows targeting 1.0
	handle11, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle12, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// Now add the 2.0 version
	err = ts.client.UpdateWorkerBuildIDCompatability(ctx, &client.UpdateWorkerBuildIDCompatabilityOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "2.0",
	})
	ts.NoError(err)

	// 2.0 workflows
	handle21, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle22, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// finish them all
	ts.NoError(ts.client.SignalWorkflow(ctx, handle11.GetID(), handle11.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle12.GetID(), handle12.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle21.GetID(), handle21.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle22.GetID(), handle22.GetRunID(), "start-signal", ""))

	// Wait for all wfs to finish
	ts.NoError(handle11.Get(ctx, nil))
	ts.NoError(handle12.Get(ctx, nil))
	ts.NoError(handle21.Get(ctx, nil))
	ts.NoError(handle22.Get(ctx, nil))

	// TODO: Actually assert they ran on the appropriate workers, once David's changes are ready
}
