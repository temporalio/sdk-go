package metrics

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
)

func Test_TaggedScope(t *testing.T) {
	taggedScope, closer, reporter := NewTaggedMetricsScope()
	scope := taggedScope.GetTaggedScope("tag1", "val1")
	scope.Counter("test-name").Inc(3)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.Counts()))
	require.Equal(t, int64(3), reporter.Counts()[0].Value())

	m := &sync.Map{}
	taggedScope, closer, reporter = NewTaggedMetricsScope()
	taggedScope.Map = m
	scope = taggedScope.GetTaggedScope("tag2", "val1")
	scope.Counter("test-name").Inc(2)
	taggedScope, closer2, reporter2 := NewTaggedMetricsScope()
	taggedScope.Map = m
	scope = taggedScope.GetTaggedScope("tag2", "val1")
	scope.Counter("test-name").Inc(1)
	_ = closer2.Close()
	require.Equal(t, 0, len(reporter2.Counts()))
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.Counts()))
	require.Equal(t, int64(3), reporter.Counts()[0].Value())
}

func Test_TaggedScope_WithMultiTags(t *testing.T) {
	taggedScope, closer, reporter := newTaggedMetricsScope()
	scope := taggedScope.GetTaggedScope("tag1", "val1", "tag2", "val2")
	scope.Counter("test-name").Inc(3)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.counts))
	require.Equal(t, int64(3), reporter.counts[0].value)

	m := &sync.Map{}
	taggedScope, closer, reporter = newTaggedMetricsScope()
	taggedScope.Map = m
	scope = taggedScope.GetTaggedScope("tag2", "val1", "tag3", "val3")
	scope.Counter("test-name").Inc(2)
	taggedScope, closer2, reporter2 := newTaggedMetricsScope()
	taggedScope.Map = m
	scope = taggedScope.GetTaggedScope("tag2", "val1", "tag3", "val3")
	scope.Counter("test-name").Inc(1)
	_ = closer2.Close()
	require.Equal(t, 0, len(reporter2.counts))
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.counts))
	require.Equal(t, int64(3), reporter.counts[0].value)

	//lint:ignore SA5012 as we test exact for this
	require.Panics(t, func() { taggedScope.GetTaggedScope("tag") })
}

func newMetricsScope(isReplay *bool) (tally.Scope, io.Closer, *capturingStatsReporter) {
	reporter := &capturingStatsReporter{}
	opts := tally.ScopeOptions{Reporter: reporter}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &realClock{}), closer, reporter
}

func newTaggedMetricsScope() (*TaggedScope, io.Closer, *capturingStatsReporter) {
	isReplay := false
	scope, closer, reporter := newMetricsScope(&isReplay)
	return &TaggedScope{Scope: scope}, closer, reporter
}
