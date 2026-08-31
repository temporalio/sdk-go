// Package codecerror carries the internal tag the SDK applies when a
// PayloadCodec running workflow-side code requests that the current Workflow
// Task fail rather than the Workflow Execution.
//
// The tag is an unexported concrete type constructible only through Tag, so
// Extract proves the error actually originated at the PayloadCodec boundary
// instead of merely matching a method or interface any error could satisfy.
// The package intentionally depends on nothing but errors so it can be imported
// from both the converter boundary and the worker internals without cycles.
package codecerror

import "errors"

// tag marks an error as a codec-originated Workflow Task failure request. It
// unwraps to the original error so errors.Is/As and failure conversion are
// unaffected.
type tag struct {
	err error
}

func (e *tag) Error() string { return e.err.Error() }
func (e *tag) Unwrap() error { return e.err }

// Tag wraps err so Extract can later recognize it as a codec-originated
// Workflow Task failure request. Callers apply it only at the PayloadCodec
// boundary; err must be non-nil.
func Tag(err error) error {
	return &tag{err: err}
}

// Extract reports whether err carries the codec origin tag and, when it does,
// returns the tagged error so the caller can surface the underlying cause.
func Extract(err error) (error, bool) {
	var t *tag
	if errors.As(err, &t) {
		return t.err, true
	}
	return nil, false
}
