package opentelemetry

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// SpanTree returns finished spans as an indented parent/child tree for tests.
//
// NOTE: Experimental
func SpanTree(spans []sdktrace.ReadOnlySpan) []string {
	var out []string
	var walk func(parentID trace.SpanID, indent string)
	walk = func(parentID trace.SpanID, indent string) {
		for _, s := range spans {
			if s.Parent().SpanID() != parentID {
				continue
			}
			out = append(out, indent+s.Name())
			walk(s.SpanContext().SpanID(), indent+"  ")
		}
	}
	walk(trace.SpanID{}, "")
	return out
}
