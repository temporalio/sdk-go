package internal

import (
	"context"

	commonpb "go.temporal.io/temporal-proto/common"
)

// HeaderWriter is an interface to write information to temporal headers
type HeaderWriter interface {
	Set(string, []byte)
}

// HeaderReader is an interface to read information from temporal headers
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
	header *commonpb.Header
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
func NewHeaderReader(header *commonpb.Header) HeaderReader {
	return &headerReader{header}
}

type headerWriter struct {
	header *commonpb.Header
}

func (hw *headerWriter) Set(key string, value []byte) {
	if hw.header == nil {
		return
	}
	hw.header.Fields[key] = value
}

// NewHeaderWriter returns a header writer interface
func NewHeaderWriter(header *commonpb.Header) HeaderWriter {
	if header != nil && header.Fields == nil {
		header.Fields = make(map[string][]byte)
	}
	return &headerWriter{header}
}
