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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally/v4"
)

func Test_TaggedScope(t *testing.T) {
	scope, closer, reporter := NewTaggedMetricsScope()
	taggedScope := TagScope(scope, "tag1", "val1")
	taggedScope.Counter("test-name").Inc(3)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.Counts()))
	require.Equal(t, int64(3), reporter.Counts()[0].Value())

	scope, closer, reporter = NewTaggedMetricsScope()
	taggedScope = TagScope(scope, "tag2", "val1")
	taggedScope.Counter("test-name").Inc(2)

	taggedScope2 := TagScope(scope, "tag2", "val1")
	taggedScope2.Counter("test-name").Inc(1)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.Counts()))
	require.Equal(t, int64(3), reporter.Counts()[0].Value())
}

func Test_TaggedScope_WithMultiTags(t *testing.T) {
	scope, closer, reporter := newTaggedMetricsScope()
	taggedScope := TagScope(scope, "tag1", "val1", "tag2", "val2")
	taggedScope.Counter("test-name").Inc(3)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.counts))
	require.Equal(t, int64(3), reporter.counts[0].value)

	scope, closer, reporter = newTaggedMetricsScope()
	taggedScope = TagScope(scope, "tag2", "val1", "tag3", "val3")
	taggedScope.Counter("test-name").Inc(2)
	scope2 := TagScope(scope, "tag2", "val1", "tag3", "val3")
	scope2.Counter("test-name").Inc(1)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.counts))
	require.Equal(t, int64(3), reporter.counts[0].value)

	//lint:ignore SA5012 We test exactly for this for this
	require.Panics(t, func() { TagScope(scope, "tag") })
}

func newMetricsScope(isReplay *bool) (tally.Scope, io.Closer, *capturingStatsReporter) {
	reporter := &capturingStatsReporter{}
	opts := tally.ScopeOptions{Reporter: reporter}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &realClock{}), closer, reporter
}

func newTaggedMetricsScope() (tally.Scope, io.Closer, *capturingStatsReporter) {
	isReplay := false
	scope, closer, reporter := newMetricsScope(&isReplay)
	return scope, closer, reporter
}
