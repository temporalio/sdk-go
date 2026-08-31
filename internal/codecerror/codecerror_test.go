package codecerror

import (
	"errors"
	"fmt"
	"testing"
)

func TestTagExtractRoundTrip(t *testing.T) {
	cause := errors.New("boom")
	tagged := Tag(cause)

	got, ok := Extract(tagged)
	if !ok {
		t.Fatal("Extract did not recognize a tagged error")
	}
	if got != cause {
		t.Fatalf("Extract returned %v, want %v", got, cause)
	}
	if !errors.Is(tagged, cause) {
		t.Fatal("errors.Is should traverse the tag to the cause")
	}
}

func TestExtractThroughWrapper(t *testing.T) {
	cause := errors.New("boom")
	wrapped := fmt.Errorf("context: %w", Tag(cause))

	if _, ok := Extract(wrapped); !ok {
		t.Fatal("Extract should traverse an fmt.Errorf %w wrapper")
	}
}

// A foreign error that merely looks like a task-failure request must not be
// extractable: only errors produced by Tag carry the concrete origin marker.
func TestExtractRejectsForeignError(t *testing.T) {
	if _, ok := Extract(lookalikeError{}); ok {
		t.Fatal("Extract must not recognize an error it did not tag")
	}
	if _, ok := Extract(errors.New("plain")); ok {
		t.Fatal("Extract must not recognize an untagged error")
	}
}

type lookalikeError struct{}

func (lookalikeError) Error() string                     { return "look-alike" }
func (lookalikeError) RequestsWorkflowTaskFailure() bool { return true }
