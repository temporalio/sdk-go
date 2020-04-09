package metrics

import (
	"io"
	"time"

	"github.com/uber-go/tally"
)

// NewMetricsScope returns a new metric scope
func NewMetricsScope(isReplay *bool) (tally.Scope, io.Closer, *CapturingStatsReporter) {
	reporter := &CapturingStatsReporter{}
	opts := tally.ScopeOptions{Reporter: reporter}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &realClock{}), closer, reporter
}

// NewTaggedMetricsScope return NewTaggedMetricsScope
func NewTaggedMetricsScope() (*TaggedScope, io.Closer, *CapturingStatsReporter) {
	isReplay := false
	scope, closer, reporter := NewMetricsScope(&isReplay)
	return &TaggedScope{Scope: scope}, closer, reporter
}

type realClock struct {
}

func (c *realClock) Now() time.Time {
	return time.Now()
}

// CapturingStatsReporter is a reporter used by tests to capture the metric so we can verify our tests.
type CapturingStatsReporter struct {
	counts                   []CapturedCount
	gauges                   []CapturedGauge
	timers                   []CapturedTimer
	histogramValueSamples    []CapturedHistogramValueSamples
	histogramDurationSamples []CapturedHistogramDurationSamples
	capabilities             int
	flush                    int
}

// HistogramDurationSamples return HistogramDurationSamples
func (c *CapturingStatsReporter) HistogramDurationSamples() []CapturedHistogramDurationSamples {
	return c.histogramDurationSamples
}

// HistogramValueSamples return HistogramValueSamples
func (c *CapturingStatsReporter) HistogramValueSamples() []CapturedHistogramValueSamples {
	return c.histogramValueSamples
}

// Timers return Timers
func (c *CapturingStatsReporter) Timers() []CapturedTimer {
	return c.timers
}

// Gauges return Gauges
func (c *CapturingStatsReporter) Gauges() []CapturedGauge {
	return c.gauges
}

// Counts return Counts
func (c *CapturingStatsReporter) Counts() []CapturedCount {
	return c.counts
}

// CapturedCount has associated name, tags and value
type CapturedCount struct {
	name  string
	tags  map[string]string
	value int64
}

// Value return the value of CapturedCount
func (c *CapturedCount) Value() int64 {
	return c.value
}

// Tags return CapturedCount tags
func (c *CapturedCount) Tags() map[string]string {
	return c.tags
}

// Name return the name of CapturedCount
func (c *CapturedCount) Name() string {
	return c.name
}

// CapturedGauge has CapturedGauge name, tag and values
type CapturedGauge struct {
	name  string
	tags  map[string]string
	value float64
}

// Value return the value of CapturedGauge
func (c *CapturedGauge) Value() float64 {
	return c.value
}

// Tags return the tags of CapturedGauge
func (c *CapturedGauge) Tags() map[string]string {
	return c.tags
}

// Name return the name of CapturedGauge
func (c *CapturedGauge) Name() string {
	return c.name
}

// CapturedTimer has related name , tags and value
type CapturedTimer struct {
	name  string
	tags  map[string]string
	value time.Duration
}

// Value return the value of CapturedTimer
func (c *CapturedTimer) Value() time.Duration {
	return c.value
}

// Tags return the tag of CapturedTimer
func (c *CapturedTimer) Tags() map[string]string {
	return c.tags
}

// Name return the name of CapturedTimer
func (c *CapturedTimer) Name() string {
	return c.name
}

// CapturedHistogramValueSamples has related information for CapturedHistogramValueSamples
type CapturedHistogramValueSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound float64
	bucketUpperBound float64
	samples          int64
}

// CapturedHistogramDurationSamples has related information for CapturedHistogramDurationSamples
type CapturedHistogramDurationSamples struct {
	name             string
	tags             map[string]string
	bucketLowerBound time.Duration
	bucketUpperBound time.Duration
	samples          int64
}

// ReportCounter reports the counts
func (c *CapturingStatsReporter) ReportCounter(
	name string,
	tags map[string]string,
	value int64,
) {
	c.counts = append(c.counts, CapturedCount{name, tags, value})
}

// ReportGauge reports the gauges
func (c *CapturingStatsReporter) ReportGauge(
	name string,
	tags map[string]string,
	value float64,
) {
	c.gauges = append(c.gauges, CapturedGauge{name, tags, value})
}

// ReportTimer reports timers
func (c *CapturingStatsReporter) ReportTimer(
	name string,
	tags map[string]string,
	value time.Duration,
) {
	c.timers = append(c.timers, CapturedTimer{name, tags, value})
}

// ReportHistogramValueSamples reports histogramValueSamples
func (c *CapturingStatsReporter) ReportHistogramValueSamples(
	name string,
	tags map[string]string,
	_ tally.Buckets,
	bucketLowerBound,
	bucketUpperBound float64,
	samples int64,
) {
	elem := CapturedHistogramValueSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	c.histogramValueSamples = append(c.histogramValueSamples, elem)
}

// ReportHistogramDurationSamples reports ReportHistogramDurationSamples
func (c *CapturingStatsReporter) ReportHistogramDurationSamples(
	name string,
	tags map[string]string,
	_ tally.Buckets,
	bucketLowerBound,
	bucketUpperBound time.Duration,
	samples int64,
) {
	elem := CapturedHistogramDurationSamples{name, tags,
		bucketLowerBound, bucketUpperBound, samples}
	c.histogramDurationSamples = append(c.histogramDurationSamples, elem)
}

// Capabilities return tally.Capabilities
func (c *CapturingStatsReporter) Capabilities() tally.Capabilities {
	c.capabilities++
	return c
}

// Reporting will always return true
func (c *CapturingStatsReporter) Reporting() bool {
	return true
}

// Tagging will always return true
func (c *CapturingStatsReporter) Tagging() bool {
	return true
}

// Flush will add one to flush
func (c *CapturingStatsReporter) Flush() {
	c.flush++
}
