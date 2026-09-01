package codecerror

import (
	"errors"
	"fmt"
	"testing"
)

func TestTagHasRoundTrip(t *testing.T) {
	cause := errors.New("boom")
	tagged := Tag(cause)

	if !Has(tagged) {
		t.Fatal("Has did not recognize a tagged error")
	}
	if !errors.Is(tagged, cause) {
		t.Fatal("errors.Is should traverse the tag to the cause")
	}
}

func TestHasThroughWrapper(t *testing.T) {
	cause := errors.New("boom")
	wrapped := fmt.Errorf("context: %w", Tag(cause))

	if !Has(wrapped) {
		t.Fatal("Has should traverse an fmt.Errorf %w wrapper")
	}
}

// A foreign error that merely looks like a task-failure request must not be
// recognized: only errors produced by Tag carry the concrete origin marker.
func TestHasRejectsForeignError(t *testing.T) {
	if Has(lookalikeError{}) {
		t.Fatal("Has must not recognize an error it did not tag")
	}
	if Has(errors.New("plain")) {
		t.Fatal("Has must not recognize an untagged error")
	}
}

type lookalikeError struct{}

func (lookalikeError) Error() string                     { return "look-alike" }
func (lookalikeError) RequestsWorkflowTaskFailure() bool { return true }
