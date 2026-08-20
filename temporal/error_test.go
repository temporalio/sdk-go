package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewPayloadValidationError(t *testing.T) {
	violations := []map[string]string{
		{"path": "user.age", "reason": "must be an integer"},
		{"path": "user.name", "reason": "is required"},
	}

	err := NewPayloadValidationError(violations)
	var applicationErr *ApplicationError
	require.ErrorAs(t, err, &applicationErr)
	require.Equal(t, "Payload validation failed", applicationErr.Message())
	require.Equal(t, "PayloadValidationError", applicationErr.Type())
	require.True(t, applicationErr.NonRetryable())

	var gotViolations []map[string]string
	require.NoError(t, applicationErr.Details(&gotViolations))
	require.Equal(t, violations, gotViolations)
}
