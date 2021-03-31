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

package converter

import (
	commonpb "go.temporal.io/api/common/v1"
)

type (
	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, set DataConverter in client, through client.Options.
	// To override DataConverter for specific activity or child workflow use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	DataConverter interface {
		// ToPayload converts single value to payload.
		ToPayload(value interface{}) (*commonpb.Payload, error)
		// FromPayload converts single value from payload.
		FromPayload(payload *commonpb.Payload, valuePtr interface{}) error

		// ToPayloads converts a list of values.
		ToPayloads(value ...interface{}) (*commonpb.Payloads, error)
		// FromPayloads converts to a list of values of different types.
		// Useful for deserializing arguments of function invocations.
		FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error

		// ToString converts payload object into human readable string.
		ToString(input *commonpb.Payload) string
		// ToStrings converts payloads object into human readable strings.
		ToStrings(input *commonpb.Payloads) []string
	}

	// ActivityContext defines the interface we expect for Activity contexts
	ActivityContext interface {
		Value(interface{}) interface{}
	}

	// WorkflowContext defines the interface we expect for Workflow contexts.
	// This will usually be a workflow.Context but may be a context.Context in some
	// code paths such as StartWorkflow, where a workflow.Context does not exist.
	WorkflowContext interface {
		Value(interface{}) interface{}
	}

	// ContextAware is an optional interface that can be implemented alongside DataConverter.
	// This interface allows Temporal to pass Workflow/Activity contexts to the DataConverter
	// so that it may tailor it's behaviour.
	ContextAware interface {
		WithWorkflowContext(WorkflowContext) DataConverter
		WithActivityContext(ActivityContext) DataConverter
	}
)

// WithWorkflowContext returns a new DataConverter tailored to the passed Workflow context if
// the DataConverter implements the ContextAware interface. Otherwise the DataConverter is returned
// as-is.
func WithWorkflowContext(dc DataConverter, ctx WorkflowContext) DataConverter {
	if dcwv, ok := dc.(ContextAware); ok {
		return dcwv.WithWorkflowContext(ctx)
	}

	return dc
}

// WithWorkflowContext returns a new DataConverter tailored to the passed Activity context if
// the DataConverter implements the ContextAware interface. Otherwise the DataConverter is returned
// as-is.
func WithActivityContext(dc DataConverter, ctx ActivityContext) DataConverter {
	if dcwv, ok := dc.(ContextAware); ok {
		return dcwv.WithActivityContext(ctx)
	}

	return dc
}
