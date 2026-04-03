package internal

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	ilog "go.temporal.io/sdk/internal/log"
)

func TestPayloadLimitOptionsToLimits(t *testing.T) {
	t.Run("default value when zero", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{})
		require.NoError(t, err)
		require.Equal(t, int64(512*1024), limits.payloadSize)
	})

	t.Run("custom value", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: 1024})
		require.NoError(t, err)
		require.Equal(t, int64(1024), limits.payloadSize)
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: -1})
		require.Error(t, err)
	})
}

func makeTestPayload(size int) *commonpb.Payload {
	return &commonpb.Payload{
		Data: make([]byte, size),
	}
}

func TestPayloadLimitsVisitorWarning(t *testing.T) {
	t.Run("no warning when under limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 1024}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100)})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Empty(t, logger.Lines())
	})

	t.Run("warning when over limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 100}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.True(t, slices.ContainsFunc(logger.Lines(), func(line string) bool {
			return strings.Contains(line, "WARN  [TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
		}))
	})

	t.Run("no warning at exactly the limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		// Create a payload and measure its actual proto size to set limit exactly
		p := makeTestPayload(100)
		exactSize := int64(p.Size())
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: exactSize}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{p})
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("nil logger does not panic", func(t *testing.T) {
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, nil)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.NoError(t, err)
		require.Len(t, result, 1)
	})

	t.Run("zero warning limit disables warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 0}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(10000)})
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})
}

func TestPayloadLimitsVisitorError(t *testing.T) {
	t.Run("error when over error limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 100})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "error limit")
		require.Greater(t, pse.size, int64(0))
		require.Equal(t, int64(100), pse.limit)
	})

	t.Run("no error when under error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100)})
		require.NoError(t, err)
	})

	t.Run("no error at exactly the error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		p := makeTestPayload(100)
		exactSize := int64(p.Size())
		setErrorLimits(&payloadLimits{payloadSize: exactSize})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{p})
		require.NoError(t, err)
	})

	t.Run("error limits nil means no error check", func(t *testing.T) {
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100000)})
		require.NoError(t, err)
	})

	t.Run("zero error limit means no error check", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 0})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100000)})
		require.NoError(t, err)
	})

	t.Run("changed error limit allows larger payloads", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)

		setErrorLimits(&payloadLimits{payloadSize: 1000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(2000)})
		require.Error(t, err)

		setErrorLimits(&payloadLimits{payloadSize: 100000})
		_, err = visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(2000)})
		require.NoError(t, err)
	})
}

func TestPayloadLimitsVisitorAggregation(t *testing.T) {
	t.Run("sums multiple payloads", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		// Each payload is small individually, but sum exceeds limit
		setErrorLimits(&payloadLimits{payloadSize: 100})
		ctx := &proxy.VisitPayloadsContext{}
		payloads := []*commonpb.Payload{
			makeTestPayload(30),
			makeTestPayload(30),
			makeTestPayload(30),
			makeTestPayload(30),
		}
		_, err := visitor.Visit(ctx, payloads)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("nil payloads in slice are skipped", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{nil, makeTestPayload(10), nil})
		require.NoError(t, err)
	})

	t.Run("empty slice", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{})
		require.NoError(t, err)
		require.Empty(t, result)
	})
}

func TestPayloadLimitsVisitorErrorBeforeWarning(t *testing.T) {
	// When both error and warning limits are exceeded, error takes priority
	logger := ilog.NewMemoryLogger()
	visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 50}, logger)
	setErrorLimits(&payloadLimits{payloadSize: 100})
	ctx := &proxy.VisitPayloadsContext{}
	_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
	require.Error(t, err)
	// Warning should not be logged since error short-circuits
	require.Empty(t, logger.Lines())
}
