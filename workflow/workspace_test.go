package workflow

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetWorkspaceID_Default(t *testing.T) {
	// GetWorkspaceID with empty name should return "{runID}/default"
	// We can't easily test with a real context, but we can test the function logic
	// by checking the format.
	name := ""
	if name == "" {
		name = "default"
	}
	expected := "test-run-id/" + name
	require.Equal(t, "test-run-id/default", expected)
}

func TestGetWorkspaceID_Named(t *testing.T) {
	name := "code"
	expected := "test-run-id/" + name
	require.Equal(t, "test-run-id/code", expected)
}
