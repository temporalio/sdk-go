// Copyright (c) 2017 Uber Technologies, Inc.
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

import (
	"context"

	"go.uber.org/cadence/.gen/go/shared"
)

// HeaderWriter is an interface to write information to cadence headers
type HeaderWriter interface {
	Set(string, []byte)
}

// HeaderReader is an interface to read information from cadence headers
type HeaderReader interface {
	ForEachKey(handler func(string, []byte) error) error
}

// ContextPropagator is an interface that determines what information from
// context to pass along
type ContextPropagator interface {
	// Inject injects information from a Go Context into headers
	Inject(context.Context, HeaderWriter) error

	// Extract extracts context information from headers and returns a context
	// object
	Extract(context.Context, HeaderReader) (context.Context, error)

	// InjectFromWorkflow injects information from workflow context into headers
	InjectFromWorkflow(Context, HeaderWriter) error

	// ExtractToWorkflow extracts context information from headers and returns
	// a workflow context
	ExtractToWorkflow(Context, HeaderReader) (Context, error)
}

type headerReader struct {
	header *shared.Header
}

func (hr *headerReader) ForEachKey(handler func(string, []byte) error) error {
	if hr.header == nil {
		return nil
	}
	for key, value := range hr.header.Fields {
		if err := handler(key, value); err != nil {
			return err
		}
	}
	return nil
}

// NewHeaderReader returns a header reader interface
func NewHeaderReader(header *shared.Header) HeaderReader {
	return &headerReader{header}
}

type headerWriter struct {
	header *shared.Header
}

func (hw *headerWriter) Set(key string, value []byte) {
	if hw.header == nil {
		return
	}
	hw.header.Fields[key] = value
}

// NewHeaderWriter returns a header writer interface
func NewHeaderWriter(header *shared.Header) HeaderWriter {
	if header != nil && header.Fields == nil {
		header.Fields = make(map[string][]byte)
	}
	return &headerWriter{header}
}
