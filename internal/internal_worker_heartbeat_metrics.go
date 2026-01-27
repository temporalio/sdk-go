package internal

import (
	"sync/atomic"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/internal/common/metrics"
)

type heartbeatMetric int

const (
	metricStickyCacheHit heartbeatMetric = iota
	metricStickyCacheMiss
	metricStickyCacheSize

	metricWorkflowTaskFailures
	metricActivityTaskFailures
	metricLocalActivityTaskFailures
	metricNexusTaskFailures

	metricWorkflowSlotsAvailable
	metricWorkflowSlotsUsed
	metricActivitySlotsAvailable
	metricActivitySlotsUsed
	metricLocalActivitySlotsAvailable
	metricLocalActivitySlotsUsed
	metricNexusSlotsAvailable
	metricNexusSlotsUsed

	metricWorkflowTasksProcessed
	metricActivityTasksProcessed
	metricLocalActivityTasksProcessed
	metricNexusTasksProcessed

	metricWorkflowPollerCount
	metricWorkflowStickyPollerCount
	metricActivityPollerCount
	metricNexusPollerCount

	metricWorkflowLastPoll
	metricWorkflowStickyLastPoll
	metricActivityLastPoll
	metricNexusLastPoll

	metricCount
)

var counterMetricMap = map[string]heartbeatMetric{
	metrics.StickyCacheHit:                      metricStickyCacheHit,
	metrics.StickyCacheMiss:                     metricStickyCacheMiss,
	metrics.WorkflowTaskExecutionFailureCounter: metricWorkflowTaskFailures,
	metrics.ActivityExecutionFailedCounter:      metricActivityTaskFailures,
	metrics.LocalActivityExecutionFailedCounter: metricLocalActivityTaskFailures,
	metrics.NexusTaskExecutionFailedCounter:     metricNexusTaskFailures,
}

var timerMetricMap = map[string]heartbeatMetric{
	metrics.WorkflowTaskExecutionLatency:  metricWorkflowTasksProcessed,
	metrics.ActivityExecutionLatency:      metricActivityTasksProcessed,
	metrics.LocalActivityExecutionLatency: metricLocalActivityTasksProcessed,
	metrics.NexusTaskExecutionLatency:     metricNexusTasksProcessed,
}

var slotsAvailableByWorkerType = map[string]heartbeatMetric{
	"WorkflowWorker":      metricWorkflowSlotsAvailable,
	"ActivityWorker":      metricActivitySlotsAvailable,
	"LocalActivityWorker": metricLocalActivitySlotsAvailable,
	"NexusWorker":         metricNexusSlotsAvailable,
}

var slotsUsedByWorkerType = map[string]heartbeatMetric{
	"WorkflowWorker":      metricWorkflowSlotsUsed,
	"ActivityWorker":      metricActivitySlotsUsed,
	"LocalActivityWorker": metricLocalActivitySlotsUsed,
	"NexusWorker":         metricNexusSlotsUsed,
}

var pollerCountByPollerType = map[string]heartbeatMetric{
	metrics.PollerTypeWorkflowTask:       metricWorkflowPollerCount,
	metrics.PollerTypeWorkflowStickyTask: metricWorkflowStickyPollerCount,
	metrics.PollerTypeActivityTask:       metricActivityPollerCount,
	metrics.PollerTypeNexusTask:          metricNexusPollerCount,
}

// HeartbeatMetricsHandler wraps a metrics handler and captures specific metrics
// in memory that are needed for worker heartbeats
type HeartbeatMetricsHandler struct {
	underlying metrics.Handler
	workerType string
	pollerType string
	metrics    map[heartbeatMetric]*atomic.Uint64
}

// NewHeartbeatMetricsHandler creates a new handler that captures specific metrics
// for worker heartbeats while passing all metrics to the underlying handler.
func NewHeartbeatMetricsHandler(underlying metrics.Handler) *HeartbeatMetricsHandler {
	m := make(map[heartbeatMetric]*atomic.Uint64, metricCount)
	for i := range heartbeatMetric(metricCount) {
		m[i] = new(atomic.Uint64)
	}
	return &HeartbeatMetricsHandler{
		underlying: underlying,
		metrics:    m,
	}
}

func (h *HeartbeatMetricsHandler) WithTags(tags map[string]string) metrics.Handler {
	cpy := *h
	cpy.underlying = h.underlying.WithTags(tags)
	if wt, ok := tags[metrics.WorkerTypeTagName]; ok {
		cpy.workerType = wt
	}
	if pt, ok := tags[metrics.PollerTypeTagName]; ok {
		cpy.pollerType = pt
	}
	return &cpy
}

func (h *HeartbeatMetricsHandler) Counter(name string) metrics.Counter {
	underlying := h.underlying.Counter(name)
	if metric, ok := counterMetricMap[name]; ok {
		return &capturingCounter{
			underlying: underlying,
			value:      h.metrics[metric],
		}
	}
	return underlying
}

func (h *HeartbeatMetricsHandler) Gauge(name string) metrics.Gauge {
	underlying := h.underlying.Gauge(name)

	switch name {
	case metrics.StickyCacheSize:
		return &capturingGauge{
			underlying: underlying,
			value:      h.metrics[metricStickyCacheSize],
		}
	case metrics.WorkerTaskSlotsAvailable:
		if metric, ok := slotsAvailableByWorkerType[h.workerType]; ok {
			return &capturingGauge{
				underlying: underlying,
				value:      h.metrics[metric],
			}
		}
	case metrics.WorkerTaskSlotsUsed:
		if metric, ok := slotsUsedByWorkerType[h.workerType]; ok {
			return &capturingGauge{
				underlying: underlying,
				value:      h.metrics[metric],
			}
		}
	case metrics.NumPoller:
		if metric, ok := pollerCountByPollerType[h.pollerType]; ok {
			return &capturingGauge{
				underlying: underlying,
				value:      h.metrics[metric],
			}
		}
	}

	return underlying
}

func (h *HeartbeatMetricsHandler) Timer(name string) metrics.Timer {
	underlying := h.underlying.Timer(name)
	if metric, ok := timerMetricMap[name]; ok {
		return &capturingTimer{
			underlying: underlying,
			counter:    h.metrics[metric],
		}
	}
	return underlying
}

// PopulateHeartbeatOptions contains external dependencies needed to populate heartbeat metrics.
type PopulateHeartbeatOptions struct {
	WorkflowSlotSupplierKind      string
	ActivitySlotSupplierKind      string
	LocalActivitySlotSupplierKind string
	NexusSlotSupplierKind         string

	WorkflowPollerBehavior PollerBehavior
	ActivityPollerBehavior PollerBehavior
	NexusPollerBehavior    PollerBehavior

	// For delta calculations between heartbeats (mutated by PopulateHeartbeat)
	PrevWorkflowProcessed      *int64
	PrevWorkflowFailed         *int64
	PrevActivityProcessed      *int64
	PrevActivityFailed         *int64
	PrevLocalActivityProcessed *int64
	PrevLocalActivityFailed    *int64
	PrevNexusProcessed         *int64
	PrevNexusFailed            *int64
}

// PopulateHeartbeat fills in the metrics-related fields of the passed in WorkerHeartbeat proto, as well as updates
// references in the PopulateHeartbeatOptions for future delta calculations.
func (h *HeartbeatMetricsHandler) PopulateHeartbeat(hb *workerpb.WorkerHeartbeat, opts *PopulateHeartbeatOptions) {
	hb.TotalStickyCacheHit = int32(h.metrics[metricStickyCacheHit].Load())
	hb.TotalStickyCacheMiss = int32(h.metrics[metricStickyCacheMiss].Load())
	hb.CurrentStickyCacheSize = int32(h.metrics[metricStickyCacheSize].Load())

	if opts.WorkflowSlotSupplierKind != "" {
		hb.WorkflowTaskSlotsInfo = buildSlotsInfo(
			opts.WorkflowSlotSupplierKind,
			int32(h.metrics[metricWorkflowSlotsAvailable].Load()),
			int32(h.metrics[metricWorkflowSlotsUsed].Load()),
			int64(h.metrics[metricWorkflowTasksProcessed].Load()),
			int64(h.metrics[metricWorkflowTaskFailures].Load()),
			opts.PrevWorkflowProcessed,
			opts.PrevWorkflowFailed,
		)
	}

	if opts.ActivitySlotSupplierKind != "" {
		hb.ActivityTaskSlotsInfo = buildSlotsInfo(
			opts.ActivitySlotSupplierKind,
			int32(h.metrics[metricActivitySlotsAvailable].Load()),
			int32(h.metrics[metricActivitySlotsUsed].Load()),
			int64(h.metrics[metricActivityTasksProcessed].Load()),
			int64(h.metrics[metricActivityTaskFailures].Load()),
			opts.PrevActivityProcessed,
			opts.PrevActivityFailed,
		)
	}

	if opts.LocalActivitySlotSupplierKind != "" {
		hb.LocalActivitySlotsInfo = buildSlotsInfo(
			opts.LocalActivitySlotSupplierKind,
			int32(h.metrics[metricLocalActivitySlotsAvailable].Load()),
			int32(h.metrics[metricLocalActivitySlotsUsed].Load()),
			int64(h.metrics[metricLocalActivityTasksProcessed].Load()),
			int64(h.metrics[metricLocalActivityTaskFailures].Load()),
			opts.PrevLocalActivityProcessed,
			opts.PrevLocalActivityFailed,
		)
	}

	if opts.NexusSlotSupplierKind != "" {
		hb.NexusTaskSlotsInfo = buildSlotsInfo(
			opts.NexusSlotSupplierKind,
			int32(h.metrics[metricNexusSlotsAvailable].Load()),
			int32(h.metrics[metricNexusSlotsUsed].Load()),
			int64(h.metrics[metricNexusTasksProcessed].Load()),
			int64(h.metrics[metricNexusTaskFailures].Load()),
			opts.PrevNexusProcessed,
			opts.PrevNexusFailed,
		)
	}

	hb.WorkflowPollerInfo = buildPollerInfo(
		int32(h.metrics[metricWorkflowPollerCount].Load()),
		h.getLastPollTime(metricWorkflowLastPoll),
		opts.WorkflowPollerBehavior,
	)
	hb.WorkflowStickyPollerInfo = buildPollerInfo(
		int32(h.metrics[metricWorkflowStickyPollerCount].Load()),
		h.getLastPollTime(metricWorkflowStickyLastPoll),
		opts.WorkflowPollerBehavior,
	)
	hb.ActivityPollerInfo = buildPollerInfo(
		int32(h.metrics[metricActivityPollerCount].Load()),
		h.getLastPollTime(metricActivityLastPoll),
		opts.ActivityPollerBehavior,
	)
	hb.NexusPollerInfo = buildPollerInfo(
		int32(h.metrics[metricNexusPollerCount].Load()),
		h.getLastPollTime(metricNexusLastPoll),
		opts.NexusPollerBehavior,
	)
}

func (h *HeartbeatMetricsHandler) getLastPollTime(metric heartbeatMetric) time.Time {
	nanos := h.metrics[metric].Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(nanos))
}

func buildSlotsInfo(
	supplierKind string,
	slotsAvailable int32,
	slotsUsed int32,
	totalProcessed int64,
	totalFailed int64,
	prevProcessed *int64,
	prevFailed *int64,
) *workerpb.WorkerSlotsInfo {
	intervalProcessed := totalProcessed - *prevProcessed
	intervalFailed := totalFailed - *prevFailed

	*prevProcessed = totalProcessed
	*prevFailed = totalFailed

	return &workerpb.WorkerSlotsInfo{
		CurrentAvailableSlots:      slotsAvailable,
		CurrentUsedSlots:           slotsUsed,
		SlotSupplierKind:           supplierKind,
		TotalProcessedTasks:        int32(totalProcessed),
		TotalFailedTasks:           int32(totalFailed),
		LastIntervalProcessedTasks: int32(intervalProcessed),
		LastIntervalFailureTasks:   int32(intervalFailed),
	}
}

func buildPollerInfo(currentPollers int32, lastSuccessfulPollTime time.Time, pollerBehavior PollerBehavior) *workerpb.WorkerPollerInfo {
	var isAutoscaling bool
	switch pollerBehavior.(type) {
	case *pollerBehaviorAutoscaling:
		isAutoscaling = true
	}

	return &workerpb.WorkerPollerInfo{
		CurrentPollers:         currentPollers,
		LastSuccessfulPollTime: timestamppb.New(lastSuccessfulPollTime),
		IsAutoscaling:          isAutoscaling,
	}
}

// RecordWorkflowPollSuccess records a successful workflow task poll.
func (h *HeartbeatMetricsHandler) RecordWorkflowPollSuccess() {
	h.metrics[metricWorkflowLastPoll].Store(uint64(time.Now().UnixNano()))
}

// RecordWorkflowStickyPollSuccess records a successful workflow sticky task poll.
func (h *HeartbeatMetricsHandler) RecordWorkflowStickyPollSuccess() {
	h.metrics[metricWorkflowStickyLastPoll].Store(uint64(time.Now().UnixNano()))
}

// RecordActivityPollSuccess records a successful activity task poll.
func (h *HeartbeatMetricsHandler) RecordActivityPollSuccess() {
	h.metrics[metricActivityLastPoll].Store(uint64(time.Now().UnixNano()))
}

// RecordNexusPollSuccess records a successful nexus task poll.
func (h *HeartbeatMetricsHandler) RecordNexusPollSuccess() {
	h.metrics[metricNexusLastPoll].Store(uint64(time.Now().UnixNano()))
}

// RecordPollSuccess records a successful poll time if the handler is a *HeartbeatMetricsHandler.
// pollerType should be one of PollerTypeWorkflowTask, PollerTypeWorkflowStickyTask,
// PollerTypeActivityTask, or PollerTypeNexusTask.
func RecordPollSuccess(h metrics.Handler, pollerType string) {
	hm, ok := h.(*HeartbeatMetricsHandler)
	if !ok {
		return
	}
	switch pollerType {
	case metrics.PollerTypeWorkflowTask:
		hm.RecordWorkflowPollSuccess()
	case metrics.PollerTypeWorkflowStickyTask:
		hm.RecordWorkflowStickyPollSuccess()
	case metrics.PollerTypeActivityTask:
		hm.RecordActivityPollSuccess()
	case metrics.PollerTypeNexusTask:
		hm.RecordNexusPollSuccess()
	}
}

// capturingCounter wraps a counter and captures its value in memory for heartbeat reporting.
type capturingCounter struct {
	underlying metrics.Counter
	value      *atomic.Uint64
}

func (c *capturingCounter) Inc(delta int64) {
	c.underlying.Inc(delta)
	if delta > 0 {
		c.value.Add(uint64(delta))
	}
}

// capturingGauge wraps a gauge and captures its value in memory for heartbeat reporting.
type capturingGauge struct {
	underlying metrics.Gauge
	value      *atomic.Uint64
}

func (g *capturingGauge) Update(f float64) {
	g.underlying.Update(f)
	g.value.Store(uint64(f))
}

// capturingTimer wraps a timer and increments a counter each time Record is called.
type capturingTimer struct {
	underlying metrics.Timer
	counter    *atomic.Uint64
}

func (t *capturingTimer) Record(d time.Duration) {
	t.underlying.Record(d)
	t.counter.Add(1)
}
