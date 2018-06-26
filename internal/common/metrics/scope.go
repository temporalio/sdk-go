// Copyright (c) 2017 Uber Technologies, Inc.
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
	"sync"
	"time"

	"github.com/uber-go/tally"
)

type (
	// Clock defines interface used as time source
	Clock interface {
		Now() time.Time
	}

	durationRecorder interface {
		RecordDuration(duration time.Duration)
	}

	replayAwareScope struct {
		isReplay *bool
		scope    tally.Scope
		clock    Clock
	}

	replayAwareCounter struct {
		isReplay *bool
		counter  tally.Counter
	}

	replayAwareGauge struct {
		isReplay *bool
		gauge    tally.Gauge
	}

	replayAwareTimer struct {
		isReplay *bool
		timer    tally.Timer
		clock    Clock
	}

	replayAwareHistogram struct {
		isReplay  *bool
		histogram tally.Histogram
		clock     Clock
	}

	replayAwareStopwatchRecorder struct {
		isReplay *bool
		recorder durationRecorder
		clock    Clock
	}

	// TaggedScope provides metricScope with tags
	TaggedScope struct {
		tally.Scope
		*sync.Map
	}
)

// WrapScope wraps a scope and skip recording metrics when isReplay is true.
// This is designed to be used by only by workflowEnvironmentImpl so we suppress metrics while replaying history events.
// Parameter isReplay is a pointer to workflowEnvironmentImpl.isReplay which will be updated when replaying history events.
func WrapScope(isReplay *bool, scope tally.Scope, clock Clock) tally.Scope {
	return &replayAwareScope{isReplay, scope, clock}
}

// Inc increments the counter by a delta.
func (c *replayAwareCounter) Inc(delta int64) {
	if *c.isReplay {
		return
	}
	c.counter.Inc(delta)
}

// Update sets the gauges absolute value.
func (g *replayAwareGauge) Update(value float64) {
	if *g.isReplay {
		return
	}
	g.gauge.Update(value)
}

// Record a specific duration.
func (t *replayAwareTimer) Record(value time.Duration) {
	if *t.isReplay {
		return
	}
	t.timer.Record(value)
}

// Record a specific duration.
func (t *replayAwareTimer) RecordDuration(duration time.Duration) {
	t.Record(duration)
}

// Start gives you back a specific point in time to report via Stop.
func (t *replayAwareTimer) Start() tally.Stopwatch {
	return tally.NewStopwatch(t.clock.Now(), &replayAwareStopwatchRecorder{t.isReplay, t, t.clock})
}

// RecordValue records a specific value directly. Will use the configured value buckets for the histogram.
func (h *replayAwareHistogram) RecordValue(value float64) {
	if *h.isReplay {
		return
	}
	h.histogram.RecordValue(value)
}

// RecordDuration records a specific duration directly.
// Will use the configured duration buckets for the histogram.
func (h *replayAwareHistogram) RecordDuration(value time.Duration) {
	if *h.isReplay {
		return
	}
	h.histogram.RecordDuration(value)
}

// Start gives you a specific point in time to then record a duration.
// Will use the configured duration buckets for the histogram.
func (h *replayAwareHistogram) Start() tally.Stopwatch {
	return tally.NewStopwatch(h.clock.Now(), &replayAwareStopwatchRecorder{h.isReplay, h, h.clock})
}

// StopwatchRecorder is a recorder that is called when a stopwatch is stopped with Stop().
func (r *replayAwareStopwatchRecorder) RecordStopwatch(stopwatchStart time.Time) {
	if *r.isReplay {
		return
	}
	d := r.clock.Now().Sub(stopwatchStart)
	r.recorder.RecordDuration(d)
}

// Counter returns the Counter object corresponding to the name.
func (s *replayAwareScope) Counter(name string) tally.Counter {
	return &replayAwareCounter{s.isReplay, s.scope.Counter(name)}
}

// Gauge returns the Gauge object corresponding to the name.
func (s *replayAwareScope) Gauge(name string) tally.Gauge {
	return &replayAwareGauge{s.isReplay, s.scope.Gauge(name)}
}

// Timer returns the Timer object corresponding to the name.
func (s *replayAwareScope) Timer(name string) tally.Timer {
	return &replayAwareTimer{s.isReplay, s.scope.Timer(name), s.clock}
}

// Histogram returns the Histogram object corresponding to the name.
// To use default value and duration buckets configured for the scope
// simply pass tally.DefaultBuckets or nil.
// You can use tally.ValueBuckets{x, y, ...} for value buckets.
// You can use tally.DurationBuckets{x, y, ...} for duration buckets.
// You can use tally.MustMakeLinearValueBuckets(start, width, count) for linear values.
// You can use tally.MustMakeLinearDurationBuckets(start, width, count) for linear durations.
// You can use tally.MustMakeExponentialValueBuckets(start, factor, count) for exponential values.
// You can use tally.MustMakeExponentialDurationBuckets(start, factor, count) for exponential durations.
func (s *replayAwareScope) Histogram(name string, buckets tally.Buckets) tally.Histogram {
	return &replayAwareHistogram{s.isReplay, s.scope.Histogram(name, buckets), s.clock}
}

// Tagged returns a new child scope with the given tags and current tags.
func (s *replayAwareScope) Tagged(tags map[string]string) tally.Scope {
	return &replayAwareScope{s.isReplay, s.scope.Tagged(tags), s.clock}
}

// SubScope returns a new child scope appending a further name prefix.
func (s *replayAwareScope) SubScope(name string) tally.Scope {
	return &replayAwareScope{s.isReplay, s.scope.SubScope(name), s.clock}
}

// Capabilities returns a description of metrics reporting capabilities.
func (s *replayAwareScope) Capabilities() tally.Capabilities {
	return s.scope.Capabilities()
}

// GetTaggedScope return a scope with one or multiple tags,
// input should be key value pairs like: GetTaggedScope(scope, tag1, val1, tag2, val2).
func (ts *TaggedScope) GetTaggedScope(keyValueinPairs ...string) tally.Scope {
	if len(keyValueinPairs)%2 != 0 {
		panic("GetTaggedScope key value are not in pairs")
	}
	if ts.Map == nil {
		ts.Map = &sync.Map{}
	}

	key := ""
	tagsMap := map[string]string{}
	for i := 0; i < len(keyValueinPairs); i += 2 {
		tagName := keyValueinPairs[i]
		tagValue := keyValueinPairs[i+1]
		key = key + tagName + ":" + tagValue + "-" // used to prevent collision of tagValue (map key) for different tagName
		tagsMap[tagName] = tagValue
	}

	taggedScope, ok := ts.Load(key)
	if !ok {
		ts.Store(key, ts.Scope.Tagged(tagsMap))
		taggedScope, _ = ts.Load(key)
	}
	if taggedScope == nil {
		panic("metric scope cannot be tagged") // This should never happen
	}

	return taggedScope.(tally.Scope)
}

// NewTaggedScope create a new TaggedScope
func NewTaggedScope(scope tally.Scope) *TaggedScope {
	if scope == nil {
		scope = tally.NoopScope
	}
	return &TaggedScope{Scope: scope, Map: &sync.Map{}}
}
