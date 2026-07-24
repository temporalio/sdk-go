// Package spantree provides a test helper for formatting finished spans as an
// indented tree. It lives in internal/ so both the internal and external test
// packages can share it without exposing it in the public API.
package spantree

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Format formats finished spans as an indented tree.
func Format(spans []sdktrace.ReadOnlySpan) []string {
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
