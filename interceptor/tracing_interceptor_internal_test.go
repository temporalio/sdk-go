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

package interceptor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.temporal.io/sdk/workflow"
)

func TestIdempotencyKeyGeneration(t *testing.T) {
	i := &tracingWorkflowInboundInterceptor{
		root: nil,
		info: &workflow.Info{
			Namespace: "default",
			WorkflowExecution: workflow.Execution{
				ID:    "foo",
				RunID: "bar",
			},
		},
	}

	// Verify that each call to get an idempotency key results in a deterministic and different result.
	//
	// XXX(jlegrone): Any change to the idempotency key format in this test constitutes a breaking
	//                change to the SDK. This is because tracers that were using the old idempotency
	//                key format will generate new IDs for the same spans after updating, and that
	//                will lead to disconnected traces for workflows still running at the time of the
	//                SDK upgrade.
	//
	//                If possible, changing the idempotency keys should be avoided. Otherwise at a
	//                minimum, a description of this upgrade behavior should be included in the next
	//                SDK version's release notes.
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:1")
	assert.Equal(t, i.newIdempotencyKey(), "WorkflowInboundInterceptor:default:foo:bar:2")
}
