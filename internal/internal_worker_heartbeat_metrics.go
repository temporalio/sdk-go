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

// GetStickyCacheHit returns the total number of sticky cache hits.
func (h *HeartbeatMetricsHandler) GetStickyCacheHit() int32 {
	return int32(h.metrics[metricStickyCacheHit].Load())
}

// GetStickyCacheMiss returns the total number of sticky cache misses.
func (h *HeartbeatMetricsHandler) GetStickyCacheMiss() int32 {
	return int32(h.metrics[metricStickyCacheMiss].Load())
}

// GetStickyCacheSize returns the current sticky cache size.
func (h *HeartbeatMetricsHandler) GetStickyCacheSize() int32 {
	return int32(h.metrics[metricStickyCacheSize].Load())
}

// GetWorkflowTaskFailures returns the total number of workflow task failures.
func (h *HeartbeatMetricsHandler) GetWorkflowTaskFailures() int64 {
	return int64(h.metrics[metricWorkflowTaskFailures].Load())
}

// GetActivityTaskFailures returns the total number of activity task failures.
func (h *HeartbeatMetricsHandler) GetActivityTaskFailures() int64 {
	return int64(h.metrics[metricActivityTaskFailures].Load())
}

// GetLocalActivityTaskFailures returns the total number of local activity task failures.
func (h *HeartbeatMetricsHandler) GetLocalActivityTaskFailures() int64 {
	return int64(h.metrics[metricLocalActivityTaskFailures].Load())
}

// GetNexusTaskFailures returns the total number of nexus task failures.
func (h *HeartbeatMetricsHandler) GetNexusTaskFailures() int64 {
	return int64(h.metrics[metricNexusTaskFailures].Load())
}

// GetWorkflowSlotsAvailable returns the current workflow slots available.
func (h *HeartbeatMetricsHandler) GetWorkflowSlotsAvailable() int32 {
	return int32(h.metrics[metricWorkflowSlotsAvailable].Load())
}

// GetWorkflowSlotsUsed returns the current workflow slots used.
func (h *HeartbeatMetricsHandler) GetWorkflowSlotsUsed() int32 {
	return int32(h.metrics[metricWorkflowSlotsUsed].Load())
}

// GetActivitySlotsAvailable returns the current activity slots available.
func (h *HeartbeatMetricsHandler) GetActivitySlotsAvailable() int32 {
	return int32(h.metrics[metricActivitySlotsAvailable].Load())
}

// GetActivitySlotsUsed returns the current activity slots used.
func (h *HeartbeatMetricsHandler) GetActivitySlotsUsed() int32 {
	return int32(h.metrics[metricActivitySlotsUsed].Load())
}

// GetLocalActivitySlotsAvailable returns the current local activity slots available.
func (h *HeartbeatMetricsHandler) GetLocalActivitySlotsAvailable() int32 {
	return int32(h.metrics[metricLocalActivitySlotsAvailable].Load())
}

// GetLocalActivitySlotsUsed returns the current local activity slots used.
func (h *HeartbeatMetricsHandler) GetLocalActivitySlotsUsed() int32 {
	return int32(h.metrics[metricLocalActivitySlotsUsed].Load())
}

// GetNexusSlotsAvailable returns the current nexus slots available.
func (h *HeartbeatMetricsHandler) GetNexusSlotsAvailable() int32 {
	return int32(h.metrics[metricNexusSlotsAvailable].Load())
}

// GetNexusSlotsUsed returns the current nexus slots used.
func (h *HeartbeatMetricsHandler) GetNexusSlotsUsed() int32 {
	return int32(h.metrics[metricNexusSlotsUsed].Load())
}

// GetWorkflowTasksProcessed returns the total number of workflow tasks processed.
func (h *HeartbeatMetricsHandler) GetWorkflowTasksProcessed() int64 {
	return int64(h.metrics[metricWorkflowTasksProcessed].Load())
}

// GetActivityTasksProcessed returns the total number of activity tasks processed.
func (h *HeartbeatMetricsHandler) GetActivityTasksProcessed() int64 {
	return int64(h.metrics[metricActivityTasksProcessed].Load())
}

// GetLocalActivityTasksProcessed returns the total number of local activity tasks processed.
func (h *HeartbeatMetricsHandler) GetLocalActivityTasksProcessed() int64 {
	return int64(h.metrics[metricLocalActivityTasksProcessed].Load())
}

// GetNexusTasksProcessed returns the total number of nexus tasks processed.
func (h *HeartbeatMetricsHandler) GetNexusTasksProcessed() int64 {
	return int64(h.metrics[metricNexusTasksProcessed].Load())
}

// GetWorkflowPollerCount returns the current number of workflow task pollers.
func (h *HeartbeatMetricsHandler) GetWorkflowPollerCount() int32 {
	return int32(h.metrics[metricWorkflowPollerCount].Load())
}

// GetWorkflowStickyPollerCount returns the current number of workflow sticky task pollers.
func (h *HeartbeatMetricsHandler) GetWorkflowStickyPollerCount() int32 {
	return int32(h.metrics[metricWorkflowStickyPollerCount].Load())
}

// GetActivityPollerCount returns the current number of activity task pollers.
func (h *HeartbeatMetricsHandler) GetActivityPollerCount() int32 {
	return int32(h.metrics[metricActivityPollerCount].Load())
}

// GetNexusPollerCount returns the current number of nexus task pollers.
func (h *HeartbeatMetricsHandler) GetNexusPollerCount() int32 {
	return int32(h.metrics[metricNexusPollerCount].Load())
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

// GetWorkflowLastPollTime returns the last successful workflow task poll time.
func (h *HeartbeatMetricsHandler) GetWorkflowLastPollTime() time.Time {
	nanos := h.metrics[metricWorkflowLastPoll].Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(nanos))
}

// GetWorkflowStickyLastPollTime returns the last successful workflow sticky task poll time.
func (h *HeartbeatMetricsHandler) GetWorkflowStickyLastPollTime() time.Time {
	nanos := h.metrics[metricWorkflowStickyLastPoll].Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(nanos))
}

// GetActivityLastPollTime returns the last successful activity task poll time.
func (h *HeartbeatMetricsHandler) GetActivityLastPollTime() time.Time {
	nanos := h.metrics[metricActivityLastPoll].Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(nanos))
}

// GetNexusLastPollTime returns the last successful nexus task poll time.
func (h *HeartbeatMetricsHandler) GetNexusLastPollTime() time.Time {
	nanos := h.metrics[metricNexusLastPoll].Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(nanos))
}

// PollSuccessRecorder is an optional interface for recording successful poll times.
type PollSuccessRecorder interface {
	RecordWorkflowPollSuccess()
	RecordWorkflowStickyPollSuccess()
	RecordActivityPollSuccess()
	RecordNexusPollSuccess()
}

// RecordPollSuccess records a successful poll time if the handler supports it.
// pollerType should be one of PollerTypeWorkflowTask, PollerTypeWorkflowStickyTask,
// PollerTypeActivityTask, or PollerTypeNexusTask.
func RecordPollSuccess(h metrics.Handler, pollerType string) {
	recorder, ok := h.(PollSuccessRecorder)
	if !ok {
		return
	}
	switch pollerType {
	case metrics.PollerTypeWorkflowTask:
		recorder.RecordWorkflowPollSuccess()
	case metrics.PollerTypeWorkflowStickyTask:
		recorder.RecordWorkflowStickyPollSuccess()
	case metrics.PollerTypeActivityTask:
		recorder.RecordActivityPollSuccess()
	case metrics.PollerTypeNexusTask:
		recorder.RecordNexusPollSuccess()
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
