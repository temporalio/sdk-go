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
)

type WorkerVersioningTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
}

func TestWorkerVersioningTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerVersioningTestSuite))
}

func (ts *WorkerVersioningTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerVersioningTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerVersioningTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerVersioningTestSuite) TestManipulateVersionGraph() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	err := ts.client.UpdateWorkerBuildIDOrdering(ctx, &client.UpdateWorkerBuildIDOrderingOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "1.0",
		BecomeDefault: true,
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIDOrdering(ctx, &client.UpdateWorkerBuildIDOrderingOptions{
		TaskQueue:     ts.taskQueueName,
		WorkerBuildID: "2.0",
		BecomeDefault: true,
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIDOrdering(ctx, &client.UpdateWorkerBuildIDOrderingOptions{
		TaskQueue:          ts.taskQueueName,
		WorkerBuildID:      "1.1",
		PreviousCompatible: "1.0",
	})
	ts.NoError(err)

	res, err := ts.client.GetWorkerBuildIDOrdering(ctx, &client.GetWorkerBuildIDOrderingOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)
	ts.Equal("2.0", res.Default.WorkerBuildID)
	ts.Equal("1.1", res.CompatibleLeaves[0].WorkerBuildID)
	ts.Equal("1.0", res.CompatibleLeaves[0].PreviousCompatible.WorkerBuildID)
}
