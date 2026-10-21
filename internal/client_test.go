package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/payloadlimits"
)

func TestPayloadLimitOptionsToLimits(t *testing.T) {
	t.Run("default value when zero", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{})
		require.NoError(t, err)
		require.Equal(t, payloadlimits.Limits{BlobWarn: 512 * 1024, MemoWarn: 2 * 1024}, limits)
	})

	t.Run("custom value", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: 1024, MemoSizeWarning: 2048})
		require.NoError(t, err)
		require.Equal(t, payloadlimits.Limits{BlobWarn: 1024, MemoWarn: 2048}, limits)
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: -1})
		require.Error(t, err)
	})

	t.Run("negative memo value returns error", func(t *testing.T) {
		_, err := payloadLimitOptionsToLimits(PayloadLimitOptions{MemoSizeWarning: -1})
		require.Error(t, err)
	})
}
