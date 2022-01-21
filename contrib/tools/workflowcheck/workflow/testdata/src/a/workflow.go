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

package a //want package:"\\d+ non-deterministic vars/funcs"

import (
	"time"

	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func PrepWorkflow() {
	var wrk worker.Worker
	wrk.RegisterWorkflow(WorkflowNop)
	wrk.RegisterWorkflow(WorkflowCallTime)             // want "a.WorkflowCallTime is non-deterministic, reason: calls non-deterministic function time.Now"
	wrk.RegisterWorkflow(WorkflowCallTimeTransitively) // want "a.WorkflowCallTimeTransitively is non-deterministic, reason: calls non-deterministic function a.SomeTimeCall"
	wrk.RegisterWorkflow(WorkflowIterateMap)           // want "a.WorkflowIterateMap is non-deterministic, reason: iterates over map"
}

func WorkflowNop(ctx workflow.Context) error {
	return nil
}

func WorkflowCallTime(ctx workflow.Context) error { // want WorkflowCallTime:"calls non-deterministic function time.Now"
	time.Now()
	return nil
}

func WorkflowCallTimeTransitively(ctx workflow.Context) error { // want WorkflowCallTimeTransitively:"calls non-deterministic function a.SomeTimeCall"
	SomeTimeCall()
	return nil
}

func SomeTimeCall() time.Time { // want SomeTimeCall:"calls non-deterministic function time.Now"
	return time.Now()
}

func WorkflowIterateMap(ctx workflow.Context) error { // want WorkflowIterateMap:"iterates over map"
	var m map[string]string
	for range m {
	}
	return nil
}
