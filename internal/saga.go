// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2022 Uber Technologies, Inc.
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

package internal

// SagaOptions stores all saga-specific parameters that will be stored inside of a context.
type SagaOptions struct {
	// ParallelCompensation decides if the compensation operations are run in parallel.
	// If parallelCompensation is false, then the compensation operations will be run
	// the reverse order as they are added.
	//
	// The default value is false.
	ParallelCompensation bool

	// ContinueOnError gives user the option to bail out of compensation operations if exception
	// is thrown while running them. This is useful only when parallelCompensation is false. If
	// parallel compensation is set to true, then all the compensation operations will be fired no
	// matter what and caller will receive exceptions back if there's any.
	//
	// The default value is false.
	ContinueOnError bool
}

// WithSagaOptions adds all options to the copy of the context.
func WithSagaOptions(ctx Context, options *SagaOptions) Context {
	ctx1 := setSagaParametersIfNotExist(ctx)
	so := getSagaOptions(ctx1)

	so.ParallelCompensation = options.ParallelCompensation
	so.ContinueOnError = options.ContinueOnError

	return ctx1
}

// NewSaga creates a new Saga instance.
func NewSaga(ctx Context) *Saga {
	var (
		sagaCtx, cancelFunc = NewDisconnectedContext(ctx)
		options             = getSagaOptions(ctx)
	)

	if options == nil {
		options = &SagaOptions{}
	}

	return &Saga{
		ctx:               sagaCtx,
		cancelFunc:        cancelFunc,
		options:           options,
		compensationStack: []*compensationOp{},
	}
}
