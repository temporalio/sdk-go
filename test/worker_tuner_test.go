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

	"go.temporal.io/sdk/worker"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/contrib/resourcetuner"
)

type WorkerTunerTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	activities *Activities
}

func TestWorkerTunerTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerTunerTestSuite))
}

func (ts *WorkerTunerTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = &Activities{}
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerTunerTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerTunerTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerTunerTestSuite) TestFixedSizeWorkerTuner() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	tuner := worker.CreateFixedSizeTuner(10, 10, 5)

	ts.runTheWorkflow(tuner, ctx)
}

func (ts *WorkerTunerTestSuite) TestCompositeWorkerTuner() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	wfSS := worker.NewFixedSizeSlotSupplier(10)
	controllerOpts := resourcetuner.DefaultResourceControllerOptions()
	controllerOpts.MemTargetPercent = 0.8
	controllerOpts.CpuTargetPercent = 0.9
	controller := resourcetuner.NewResourceController(controllerOpts)
	actSS := resourcetuner.NewResourceBasedSlotSupplier(controller,
		resourcetuner.ResourceBasedSlotSupplierOptions{
			MinSlots:     10,
			MaxSlots:     20,
			RampThrottle: 0,
		})
	laCss := worker.NewFixedSizeSlotSupplier(5)
	tuner := worker.CreateCompositeTuner(wfSS, actSS, laCss)

	ts.runTheWorkflow(tuner, ctx)
}

func (ts *WorkerTunerTestSuite) runTheWorkflow(tuner worker.WorkerTuner, ctx context.Context) {
	workerOptions := worker.Options{Tuner: tuner}
	myWorker := worker.New(ts.client, ts.taskQueueName, workerOptions)
	ts.workflows.register(myWorker)
	ts.activities.register(myWorker)
	ts.NoError(myWorker.Start())
	defer myWorker.Stop()

	handle, err := ts.client.ExecuteWorkflow(ctx,
		ts.startWorkflowOptions(ts.T().Name()),
		ts.workflows.RunsLocalAndNonlocalActsWithRetries, 2)
	ts.NoError(err)
	ts.NoError(handle.Get(ctx, nil))
}
