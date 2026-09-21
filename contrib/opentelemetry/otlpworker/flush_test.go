package otlpworker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeProvider struct {
	flushes   int
	shutdowns int
	flushErr  error
}

func (p *fakeProvider) ForceFlush(context.Context) error {
	p.flushes++
	return p.flushErr
}

func (p *fakeProvider) Shutdown(context.Context) error {
	p.shutdowns++
	return nil
}

// notAFlusher implements neither ForceFlush nor Shutdown and must be ignored.
type notAFlusher struct{}

func TestForceFlush(t *testing.T) {
	a := &fakeProvider{}
	b := &fakeProvider{}
	require.NoError(t, ForceFlush(context.Background(), a, b, notAFlusher{}))
	require.Equal(t, 1, a.flushes)
	require.Equal(t, 1, b.flushes)
	require.Equal(t, 0, a.shutdowns)
}

func TestForceFlushJoinsErrors(t *testing.T) {
	boom := errors.New("boom")
	failing := &fakeProvider{flushErr: boom}
	ok := &fakeProvider{}
	require.ErrorIs(t, ForceFlush(context.Background(), failing, ok), boom)
	require.Equal(t, 1, ok.flushes)
}

func TestShutdown(t *testing.T) {
	a := &fakeProvider{}
	require.NoError(t, Shutdown(context.Background(), a, notAFlusher{}))
	require.Equal(t, 1, a.shutdowns)
	require.Equal(t, 0, a.flushes)
}

func TestFlushNoProviders(t *testing.T) {
	require.NoError(t, ForceFlush(context.Background()))
	require.NoError(t, Shutdown(context.Background()))
	require.NoError(t, ForceFlush(context.Background(), notAFlusher{}))
}
