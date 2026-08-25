package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewPayloadValidationError(t *testing.T) {
	t.Run("with details", func(t *testing.T) {
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
		require.True(t, applicationErr.HasDetails())

		var gotViolations []map[string]string
		require.NoError(t, applicationErr.Details(&gotViolations))
		require.Equal(t, violations, gotViolations)
	})

	t.Run("without details", func(t *testing.T) {
		err := NewPayloadValidationError(nil)
		var applicationErr *ApplicationError
		require.ErrorAs(t, err, &applicationErr)
		require.Equal(t, "Payload validation failed", applicationErr.Message())
		require.Equal(t, "PayloadValidationError", applicationErr.Type())
		require.True(t, applicationErr.NonRetryable())
		require.False(t, applicationErr.HasDetails())
		require.ErrorIs(t, applicationErr.Details(), ErrNoData)
	})
}
