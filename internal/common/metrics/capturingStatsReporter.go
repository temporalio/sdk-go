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
	"github.com/uber-go/tally"
	"io"
	"time"
)

func NewMetricsScope(isReplay *bool) (tally.Scope, io.Closer, *CapturingStatsReporter) {
	reporter := &CapturingStatsReporter{}
	opts := tally.ScopeOptions{Reporter: reporter}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &RealClock{}), closer, reporter
}

func NewTaggedMetricsScope() (*TaggedScope, io.Closer, *CapturingStatsReporter) {
	isReplay := false
	scope, closer, reporter := NewMetricsScope(&isReplay)
	return &TaggedScope{Scope: scope}, closer, reporter
}

type RealClock struct {
}

func (c *RealClock) Now() time.Time {
	return time.Now()
}

// capturingStatsReporter is a reporter used by tests to capture the metric so we can verify our tests.
type CapturingStatsReporter struct {
	counts                   []CapturedCount
	gauges                   []CapturedGauge
	timers                   []CapturedTimer
	histogramValueSamples    []CapturedHistogramValueSamples
	histogramDurationSamples []CapturedHistogramDurationSamples
	capabilities             int
	flush                    int
}

func (c *CapturingStatsReporter) HistogramDurationSamples() []CapturedHistogramDurationSamples {
	return c.histogramDurationSamples
}

func (c *CapturingStatsReporter) HistogramValueSamples() []CapturedHistogramValueSamples {
	return c.histogramValueSamples
}

func (c *CapturingStatsReporter) Timers() []CapturedTimer {
	return c.timers
}

func (c *CapturingStatsReporter) Gauges() []CapturedGauge {
	return c.gauges
}

func (c *CapturingStatsReporter) Counts() []CapturedCount {
	return c.counts
}

type CapturedCount struct {
	name  string
	tags  map[string]string
	value int64
}

func (c *CapturedCount) Value() int64 {
	return c.value
}

func (c *CapturedCount) Tags() map[string]string {
	return c.tags
}

func (c *CapturedCount) Name() string {
	return c.name
}

type CapturedGauge struct {
	name  string
	tags  map[string]string
	value float64
}

func (c *CapturedGauge) Value() float64 {
	return c.value
}

func (c *CapturedGauge) Tags() map[string]string {
	return c.tags
}

func (c *CapturedGauge) Name() string {
	return c.name
}

type CapturedTimer struct {
	name  string
	tags  map[string]string
	value time.Duration
}

func (c *CapturedTimer) Value() time.Duration {
	return c.value
}

func (c *CapturedTimer) Tags() map[string]string {
	return c.tags
}

func (c *CapturedTimer) Name() string {
	return c.name
}

type CapturedHistogramValueSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound float64
	bucketUpperBound float64
	samples          int64
}

type CapturedHistogramDurationSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
	samples          int64
}

func (r *CapturingStatsReporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	r.counts = append(r.counts, CapturedCount{name, tags, value})
}

func (r *CapturingStatsReporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	r.gauges = append(r.gauges, CapturedGauge{name, tags, value})
}

func (r *CapturingStatsReporter) ReportTimer(
	name string,
	tags map[string]string,
	value time.Duration,
) {
	r.timers = append(r.timers, CapturedTimer{name, tags, value})
}

func (r *CapturingStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	elem := CapturedHistogramValueSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramValueSamples = append(r.histogramValueSamples, elem)
}

func (r *CapturingStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	buckets tally.Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	elem := CapturedHistogramDurationSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	r.histogramDurationSamples = append(r.histogramDurationSamples, elem)
}

func (r *CapturingStatsReporter) Capabilities() tally.Capabilities {
	r.capabilities++
	return r
}

func (r *CapturingStatsReporter) Reporting() bool {
	return true
}

func (r *CapturingStatsReporter) Tagging() bool {
	return true
}

func (r *CapturingStatsReporter) Flush() {
	r.flush++
}
