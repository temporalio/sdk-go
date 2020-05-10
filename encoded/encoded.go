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

// Package encoded contains wrappers that are used for binary payloads deserialization.
package encoded

import "go.temporal.io/temporal/internal"

type (

	// Value is used to encapsulate/extract encoded value from workflow/activity.
	Value = internal.Value

	// Values is used to encapsulate/extract encoded one or more values from workflow/activity.
	Values = internal.Values

	// DataConverter is used by the framework to serialize/deserialize input and output of activity/workflow
	// that need to be sent over the wire.
	// To encode/decode workflow arguments, one should set DataConverter in two places:
	//   1. Workflow worker, through worker.Options
	//   2. Client, through client.Options
	// To encode/decode Activity/ChildWorkflow arguments, one should set DataConverter in two places:
	//   1. Inside workflow code, use workflow.WithDataConverter to create new Context,
	// and pass that context to ExecuteActivity/ExecuteChildWorkflow calls.
	// Temporal support using different DataConverters for different activity/childWorkflow in same workflow.
	//   2. Activity/Workflow worker that run these activity/childWorkflow, through worker.Options.
	DataConverter = internal.DataConverter

	// PayloadConverter converts single value to/from payload.
	PayloadConverter = internal.PayloadConverter
)

// GetDefaultDataConverter return default data converter used by Temporal worker
func GetDefaultDataConverter() DataConverter {
	return internal.DefaultDataConverter
}

// GetDefaultPayloadConverter return default data converter used by Temporal worker
func GetDefaultPayloadConverter() PayloadConverter {
	return internal.DefaultPayloadConverter
}
