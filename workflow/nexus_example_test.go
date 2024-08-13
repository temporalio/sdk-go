// The MIT License
//
// Copyright (c) 2024 Temporal Technologies Inc.  All rights reserved.
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

package workflow_test

import (
	"context"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporalnexus"
	"go.temporal.io/sdk/workflow"
)

type MyInput struct{}
type MyOutput struct{}

var myOperationRef = nexus.NewOperationReference[MyInput, MyOutput]("my-operation")

var myOperation = temporalnexus.NewSyncOperation("my-operation", func(ctx context.Context, c client.Client, mi MyInput, soo nexus.StartOperationOptions) (MyOutput, error) {
	return MyOutput{}, nil
})

func ExampleNexusClient() {
	myWorkflow := func(ctx workflow.Context) (MyOutput, error) {
		client := workflow.NewNexusClient("my-endpoint", "my-service")
		// Execute an operation using an operation name.
		fut := client.ExecuteOperation(ctx, "my-operation", MyInput{}, workflow.NexusOperationOptions{
			ScheduleToCloseTimeout: time.Hour,
		})
		// Or using an OperationReference.
		fut = client.ExecuteOperation(ctx, myOperationRef, MyInput{}, workflow.NexusOperationOptions{
			ScheduleToCloseTimeout: time.Hour,
		})
		// Or using a defined operation (which is also an OperationReference).
		fut = client.ExecuteOperation(ctx, myOperation, MyInput{}, workflow.NexusOperationOptions{
			ScheduleToCloseTimeout: time.Hour,
		})

		var exec workflow.NexusOperationExecution
		// Optionally wait for the operation to be started.
		_ = fut.GetNexusOperationExecution().Get(ctx, &exec)
		// OperationID will be empty if the operation completed synchronously.
		workflow.GetLogger(ctx).Info("operation started", "operationID", exec.OperationID)

		// Get the result of the operation.
		var output MyOutput
		return output, fut.Get(ctx, &output)
	}

	_ = myWorkflow
}
