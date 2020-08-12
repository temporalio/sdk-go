// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
