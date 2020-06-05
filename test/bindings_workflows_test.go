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
	"time"

	commonpb "go.temporal.io/temporal-proto/common"
	"go.temporal.io/temporal/encoded"
	bindings "go.temporal.io/temporal/internalbindings"
	"go.temporal.io/temporal/workflow"
)

type EmptyWorkflowDefinitionFactory struct {
}

func (e EmptyWorkflowDefinitionFactory) NewWorkflowDefinition() bindings.WorkflowDefinition {
	return &EmptyWorkflowDefinition{}
}

type EmptyWorkflowDefinition struct {
}

func (wd *EmptyWorkflowDefinition) Execute(env bindings.WorkflowEnvironment, header *commonpb.Header, input *commonpb.Payloads) {
	payload, err := encoded.GetDefaultDataConverter().ToPayloads("EmptyResult")
	env.Complete(payload, err)
}

func (wd *EmptyWorkflowDefinition) OnDecisionTaskStarted() {

}

func (wd *EmptyWorkflowDefinition) StackTrace() string {
	return "stackTracePlaceholder"
}

func (wd *EmptyWorkflowDefinition) Close() {

}

type SingleActivityWorkflowDefinitionFactory struct {
}

func (e SingleActivityWorkflowDefinitionFactory) NewWorkflowDefinition() bindings.WorkflowDefinition {
	return &SingleActivityWorkflowDefinition{}
}

type SingleActivityWorkflowDefinition struct {
	callbacks []func()
}

func (d *SingleActivityWorkflowDefinition) Execute(env bindings.WorkflowEnvironment, header *commonpb.Header, input *commonpb.Payloads) {
	var signalInput string
	env.RegisterSignalHandler(func(name string, input *commonpb.Payloads) {
		_ = encoded.GetDefaultDataConverter().FromPayloads(input, &signalInput)
	})
	d.callbacks = append(d.callbacks, func() {
		env.NewTimer(time.Second, d.addCallback(func(result *commonpb.Payloads, err error) {
			input, _ := encoded.GetDefaultDataConverter().ToPayloads("World")
			parameters := bindings.ExecuteActivityParams{
				ExecuteActivityOptions: bindings.ExecuteActivityOptions{
					TaskListName:               env.WorkflowInfo().TaskListName,
					StartToCloseTimeoutSeconds: 10,
					ActivityID:                 "id1",
				},
				ActivityType: bindings.ActivityType{Name: "SingleActivity"},
				Input:        input,
			}
			_ = env.ExecuteActivity(parameters, d.addCallback(func(result *commonpb.Payloads, err error) {
				childParams := bindings.ExecuteWorkflowParams{
					WorkflowOptions: bindings.WorkflowOptions{
						TaskListName: env.WorkflowInfo().TaskListName,
						WorkflowID:   "ID1",
					},
					WorkflowType: &bindings.WorkflowType{Name: "ChildWorkflow"},
					Input:        result,
				}
				env.ExecuteChildWorkflow(childParams, d.addCallback(func(r *commonpb.Payloads, err error) {
					var childResult string
					_ = encoded.GetDefaultDataConverter().FromPayloads(r, &childResult)
					result := childResult + signalInput
					encodedResult, _ := encoded.GetDefaultDataConverter().ToPayloads(result)
					env.Complete(encodedResult, err)
				}), func(r bindings.WorkflowExecution, e error) {})
			}))
		}))
	})
}

func (d *SingleActivityWorkflowDefinition) addCallback(callback bindings.ResultHandler) bindings.ResultHandler {
	return func(result *commonpb.Payloads, err error) {
		d.callbacks = append(d.callbacks, func() {
			callback(result, err)
		})
	}
}

func (d *SingleActivityWorkflowDefinition) OnDecisionTaskStarted() {
	for _, callback := range d.callbacks {
		callback()
	}
	d.callbacks = nil
}

func (d *SingleActivityWorkflowDefinition) StackTrace() string {
	panic("Not implemented")
}

func (d *SingleActivityWorkflowDefinition) Close() {
}

func SingleActivity(ctx context.Context, name string) (string, error) {
	return "Hello " + name, nil
}

func ChildWorkflow(ctx workflow.Context, greeting string) (string, error) {
	return greeting + "!", nil
}
