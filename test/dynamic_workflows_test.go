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
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"testing"
)

type DynamicWorkflowTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	activities *Activities
}

func TestDynamicWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DynamicWorkflowTestSuite))
}

func (ts *DynamicWorkflowTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = newActivities()
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *DynamicWorkflowTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *DynamicWorkflowTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func DynamicWorkflow(ctx workflow.Context, args converter.EncodedValues) (converter.EncodedValues, error) {
	fmt.Println("[DynamicWorkflow]")

	var result string
	info := workflow.GetInfo(ctx)
	if info.WorkflowType.Name == "dynamic-activity" {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{})
		err := workflow.ExecuteActivity(ctx, "random-activity-name").Get(ctx, result)
		if err != nil {
			return nil, err
		}
	} else {
		var arg1, arg2 string
		err := args.Get(&arg1, &arg2)
		if err != nil {
			return nil, fmt.Errorf("failed to decode arguments: %w", err)
		}
		fmt.Println("[DynamicWorkflow] arg1", arg1)
		fmt.Println("[DynamicWorkflow] arg2", arg2)
		fmt.Println("[DynamicWorkflow] info.WorkflowType", info.WorkflowType)

		result = fmt.Sprintf("%s - %s - %s", info.WorkflowType, arg1, arg2)
	}

	dc := converter.GetDefaultDataConverter()
	payloads, err := dc.ToPayloads(result)
	if err != nil {
		return nil, err
	}
	encodedValue := client.NewValues(payloads)
	return encodedValue, nil
}

//func DynamicActivity(ctx context.Context, args converter.EncodedValues) (string, error) {
//	//if len(args) != 2 {
//	//	return nil, fmt.Errorf("expected 2 arguments, got %d", len(args))
//	//}
//
//	var arg1 string
//	err := args[0].Get(&arg1)
//	if err != nil {
//		return "", fmt.Errorf("failed to decode first argument: %w", err)
//	}
//	var arg2 string
//	err = args[1].Get(&arg2)
//	if err != nil {
//		return "", fmt.Errorf("failed to decode second argument: %w", err)
//	}
//
//	info := activity.GetInfo(ctx)
//	result := fmt.Sprintf("%s - %s - %s", info.WorkflowType, arg1, arg2)
//	return result, nil
//
//}

func (ts *WorkerVersioningTestSuite) TestBasicDynamicWorkflowActivity() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	w := worker.New(ts.client, ts.taskQueueName, worker.Options{})
	w.RegisterDynamicWorkflow(DynamicWorkflow, workflow.DynamicRegisterOptions{})
	//w.RegisterDynamicActivity(DynamicActivity, activity.DynamicRegisterOptions{})
	fmt.Println("w", w)

	err := w.Start()
	ts.NoError(err)
	defer w.Stop()

	handle, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi"), "some-workflow", "apple", "pear")
	ts.NoError(err)
	var result string
	err = handle.Get(ctx, &result)
	fmt.Println("err", err)
	fmt.Println("result", result)
	ts.NoError(err)
	ts.Equal("some-workflow - apple - pear", result)
	//
	//handle, err = ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi1"), "dynamic-activity", "grape", "cherry")
	//ts.NoError(err)
	//err = handle.Get(ctx, &result)
	//ts.NoError(err)
	//ts.Equal("random-activity-name - grape - cherry", result)
	iter := ts.client.GetWorkflowHistory(ctx, handle.GetID(), "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		fmt.Println("event", event)
	}
}

// TODO: Query
// TODO: Signal
// TODO: Update
// TODO: Versioning per type
