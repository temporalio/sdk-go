package tracing

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteSpanHeaderFinishesSpanOnError(t *testing.T) {
	headerErr := errors.New("header error")
	span := &recordingSpan{}

	finish, err := writeSpanHeader(
		span,
		true,
		func(TracerSpanRef) error { return headerErr },
	)

	require.ErrorIs(t, err, headerErr)
	require.Nil(t, finish)
	require.Len(t, span.finished, 1)
	require.ErrorIs(t, span.finished[0].Error, headerErr)
}

type recordingSpan struct {
	finished []*TracerFinishSpanOptions
}

func (s *recordingSpan) Finish(options *TracerFinishSpanOptions) {
	s.finished = append(s.finished, options)
}
