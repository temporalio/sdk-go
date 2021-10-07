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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally/v4"
)

func Test_Counter(t *testing.T) {
	t.Parallel()
	replayed, executed := withScope(t, func(scope tally.Scope) {
		scope.Counter("test-name").Inc(3)
	})
	require.Equal(t, 0, len(replayed.Counts()))
	require.Equal(t, 1, len(executed.Counts()))
	require.Equal(t, int64(3), executed.Counts()[0].Value())
}

func Test_Gauge(t *testing.T) {
	t.Parallel()
	replayed, executed := withScope(t, func(scope tally.Scope) {
		scope.Gauge("test-name").Update(3)
	})
	require.Equal(t, 0, len(replayed.Gauges()))
	require.Equal(t, 1, len(executed.Gauges()))
	require.Equal(t, float64(3), executed.Gauges()[0].Value())
}

func Test_Timer(t *testing.T) {
	t.Parallel()
	replayed, executed := withScope(t, func(scope tally.Scope) {
		scope.Timer("test-name").Record(time.Second)
		scope.Timer("test-stopwatch").Start().Stop()
	})
	require.Equal(t, 0, len(replayed.Timers()))
	require.Equal(t, 2, len(executed.Timers()))
	require.Equal(t, time.Second, executed.Timers()[0].Value())
}

func Test_Histogram(t *testing.T) {
	t.Parallel()
	t.Run("values", func(t *testing.T) {
		t.Parallel()
		replayed, executed := withScope(t, func(scope tally.Scope) {
			valueBuckets := tally.MustMakeLinearValueBuckets(0, 10, 10)
			scope.Histogram("test-hist-1", valueBuckets).RecordValue(5)
			scope.Histogram("test-hist-2", valueBuckets).RecordValue(15)
		})
		require.Equal(t, 0, len(replayed.HistogramValueSamples()))
		require.Equal(t, 2, len(executed.HistogramValueSamples()))
	})
	t.Run("durations", func(t *testing.T) {
		t.Parallel()
		replayed, executed := withScope(t, func(scope tally.Scope) {
			durationBuckets := tally.MustMakeLinearDurationBuckets(0, time.Hour, 10)
			scope.Histogram("test-hist-1", durationBuckets).RecordDuration(time.Minute)
			scope.Histogram("test-hist-2", durationBuckets).RecordDuration(time.Minute * 61)
			scope.Histogram("test-hist-3", durationBuckets).Start().Stop()
		})
		require.Equal(t, 0, len(replayed.HistogramDurationSamples()))
		require.Equal(t, 3, len(executed.HistogramDurationSamples()))
	})
}

func Test_ScopeCoverage(t *testing.T) {
	isReplay := false
	scope, closer, reporter := NewMetricsScope(&isReplay)
	caps := scope.Capabilities()
	require.Equal(t, true, caps.Reporting())
	require.Equal(t, true, caps.Tagging())
	subScope := scope.SubScope("test")
	taggedScope := subScope.Tagged(make(map[string]string))
	taggedScope.Counter("test-counter").Inc(1)
	_ = closer.Close()
	require.Equal(t, 1, len(reporter.Counts()))
}

// withScope runs your callback twice, once for "during replay" and once for "after replay" / "executing".
// stats are captured, and the results are returned for your validation.
func withScope(t *testing.T, cb func(scope tally.Scope)) (replayed *CapturingStatsReporter, executed *CapturingStatsReporter) {
	replaying, executing := true, false

	replayingScope, replayingCloser, replayed := NewMetricsScope(&replaying)
	executingScope, executingCloser, executed := NewMetricsScope(&executing)

	defer func() {
		require.NoError(t, replayingCloser.Close())
		require.NoError(t, executingCloser.Close())
	}()

	cb(replayingScope)
	cb(executingScope)

	return replayed, executed
}

// capturingStatsReporter is a reporter used by tests to capture the metric so we can verify our tests.
type capturingStatsReporter struct {
	counts                   []capturedCount
	gauges                   []capturedGauge
	timers                   []capturedTimer
	histogramValueSamples    []capturedHistogramValueSamples
	histogramDurationSamples []capturedHistogramDurationSamples
	capabilities             int
	flush                    int
}

type capturedCount struct {
	name  string
	tags  map[string]string
	value int64
}

type capturedGauge struct {
	name  string
	tags  map[string]string
	value float64
}

type capturedTimer struct {
	name  string
	tags  map[string]string
	value time.Duration
}

type capturedHistogramValueSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound float64
	bucketUpperBound float64
	samples          int64
}

type capturedHistogramDurationSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
	samples          int64
}

func (r *capturingStatsReporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	r.counts = append(r.counts, capturedCount{name, tags, value})
}

func (r *capturingStatsReporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	r.gauges = append(r.gauges, capturedGauge{name, tags, value})
}

func (r *capturingStatsReporter) ReportTimer(
	name string,
	tags map[string]string,
	value time.Duration,
) {
	r.timers = append(r.timers, capturedTimer{name, tags, value})
}

func (r *capturingStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	_ tally.Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	elem := capturedHistogramValueSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramValueSamples = append(r.histogramValueSamples, elem)
}

func (r *capturingStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	_ tally.Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	elem := capturedHistogramDurationSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramDurationSamples = append(r.histogramDurationSamples, elem)
}

func (r *capturingStatsReporter) Capabilities() tally.Capabilities {
	r.capabilities++
	return r
}

func (r *capturingStatsReporter) Reporting() bool {
	return true
}

func (r *capturingStatsReporter) Tagging() bool {
	return true
}

func (r *capturingStatsReporter) Flush() {
	r.flush++
}
