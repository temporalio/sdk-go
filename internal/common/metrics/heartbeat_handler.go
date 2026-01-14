package metrics

import (
	"sync/atomic"
	"time"
)

// HeartbeatMetricsHandler wraps a metrics handler and captures specific metrics
// in memory that are needed for worker heartbeats
type HeartbeatMetricsHandler struct {
	underlying Handler

	// Current worker type tag for this handler instance (set via WithTags)
	workerType string

	stickyCacheHit  *atomic.Uint64
	stickyCacheMiss *atomic.Uint64
	stickyCacheSize *atomic.Uint64

	workflowTaskFailures      *atomic.Uint64
	activityTaskFailures      *atomic.Uint64
	localActivityTaskFailures *atomic.Uint64
	nexusTaskFailures         *atomic.Uint64

	workflowSlotsAvailable      *atomic.Uint64
	workflowSlotsUsed           *atomic.Uint64
	activitySlotsAvailable      *atomic.Uint64
	activitySlotsUsed           *atomic.Uint64
	localActivitySlotsAvailable *atomic.Uint64
	localActivitySlotsUsed      *atomic.Uint64
	nexusSlotsAvailable         *atomic.Uint64
	nexusSlotsUsed              *atomic.Uint64

	// Task processed counters (per worker type) - incremented each time execution latency is recorded
	workflowTasksProcessed      *atomic.Uint64
	activityTasksProcessed      *atomic.Uint64
	localActivityTasksProcessed *atomic.Uint64
	nexusTasksProcessed         *atomic.Uint64

	// Current poller type tag for this handler instance (set via WithTags)
	pollerType string

	workflowPollerCount       *atomic.Uint64
	workflowStickyPollerCount *atomic.Uint64
	activityPollerCount       *atomic.Uint64
	nexusPollerCount          *atomic.Uint64

	// Last successful poll times (per poller type) - stored as Unix nanoseconds
	// NOTE: These are only kept in memory, there is no corresponding metric exported for these.
	workflowLastPoll       *atomic.Int64
	workflowStickyLastPoll *atomic.Int64
	activityLastPoll       *atomic.Int64
	nexusLastPoll          *atomic.Int64
}

// NewHeartbeatMetricsHandler creates a new handler that captures specific metrics
// for worker heartbeats while passing all metrics to the underlying handler.
func NewHeartbeatMetricsHandler(underlying Handler) *HeartbeatMetricsHandler {
	return &HeartbeatMetricsHandler{
		underlying: underlying,

		stickyCacheHit:  new(atomic.Uint64),
		stickyCacheMiss: new(atomic.Uint64),
		stickyCacheSize: new(atomic.Uint64),

		workflowTaskFailures:      new(atomic.Uint64),
		activityTaskFailures:      new(atomic.Uint64),
		localActivityTaskFailures: new(atomic.Uint64),
		nexusTaskFailures:         new(atomic.Uint64),

		workflowSlotsAvailable:      new(atomic.Uint64),
		workflowSlotsUsed:           new(atomic.Uint64),
		activitySlotsAvailable:      new(atomic.Uint64),
		activitySlotsUsed:           new(atomic.Uint64),
		localActivitySlotsAvailable: new(atomic.Uint64),
		localActivitySlotsUsed:      new(atomic.Uint64),
		nexusSlotsAvailable:         new(atomic.Uint64),
		nexusSlotsUsed:              new(atomic.Uint64),

		workflowTasksProcessed:      new(atomic.Uint64),
		activityTasksProcessed:      new(atomic.Uint64),
		localActivityTasksProcessed: new(atomic.Uint64),
		nexusTasksProcessed:         new(atomic.Uint64),

		workflowPollerCount:       new(atomic.Uint64),
		workflowStickyPollerCount: new(atomic.Uint64),
		activityPollerCount:       new(atomic.Uint64),
		nexusPollerCount:          new(atomic.Uint64),

		workflowLastPoll:       new(atomic.Int64),
		workflowStickyLastPoll: new(atomic.Int64),
		activityLastPoll:       new(atomic.Int64),
		nexusLastPoll:          new(atomic.Int64),
	}
}

func (h *HeartbeatMetricsHandler) WithTags(tags map[string]string) Handler {
	// Track the worker type if present in tags
	workerType := h.workerType
	if wt, ok := tags[WorkerTypeTagName]; ok {
		workerType = wt
	}

	// Track the poller type if present in tags
	pollerType := h.pollerType
	if pt, ok := tags[PollerTypeTagName]; ok {
		pollerType = pt
	}

	return &HeartbeatMetricsHandler{
		underlying: h.underlying.WithTags(tags),
		workerType: workerType,
		pollerType: pollerType,

		stickyCacheHit:  h.stickyCacheHit,
		stickyCacheMiss: h.stickyCacheMiss,
		stickyCacheSize: h.stickyCacheSize,

		workflowTaskFailures:      h.workflowTaskFailures,
		activityTaskFailures:      h.activityTaskFailures,
		localActivityTaskFailures: h.localActivityTaskFailures,
		nexusTaskFailures:         h.nexusTaskFailures,

		workflowSlotsAvailable:      h.workflowSlotsAvailable,
		workflowSlotsUsed:           h.workflowSlotsUsed,
		activitySlotsAvailable:      h.activitySlotsAvailable,
		activitySlotsUsed:           h.activitySlotsUsed,
		localActivitySlotsAvailable: h.localActivitySlotsAvailable,
		localActivitySlotsUsed:      h.localActivitySlotsUsed,
		nexusSlotsAvailable:         h.nexusSlotsAvailable,
		nexusSlotsUsed:              h.nexusSlotsUsed,

		workflowTasksProcessed:      h.workflowTasksProcessed,
		activityTasksProcessed:      h.activityTasksProcessed,
		localActivityTasksProcessed: h.localActivityTasksProcessed,
		nexusTasksProcessed:         h.nexusTasksProcessed,

		workflowPollerCount:       h.workflowPollerCount,
		workflowStickyPollerCount: h.workflowStickyPollerCount,
		activityPollerCount:       h.activityPollerCount,
		nexusPollerCount:          h.nexusPollerCount,

		workflowLastPoll:       h.workflowLastPoll,
		workflowStickyLastPoll: h.workflowStickyLastPoll,
		activityLastPoll:       h.activityLastPoll,
		nexusLastPoll:          h.nexusLastPoll,
	}
}

func (h *HeartbeatMetricsHandler) Counter(name string) Counter {
	underlying := h.underlying.Counter(name)

	switch name {
	case StickyCacheHit:
		return &capturingCounter{
			underlying: underlying,
			value:      h.stickyCacheHit,
		}
	case StickyCacheMiss:
		return &capturingCounter{
			underlying: underlying,
			value:      h.stickyCacheMiss,
		}
	case WorkflowTaskExecutionFailureCounter:
		return &capturingCounter{
			underlying: underlying,
			value:      h.workflowTaskFailures,
		}
	case ActivityExecutionFailedCounter:
		return &capturingCounter{
			underlying: underlying,
			value:      h.activityTaskFailures,
		}
	case LocalActivityExecutionFailedCounter:
		return &capturingCounter{
			underlying: underlying,
			value:      h.localActivityTaskFailures,
		}
	case NexusTaskExecutionFailedCounter:
		return &capturingCounter{
			underlying: underlying,
			value:      h.nexusTaskFailures,
		}
	default:
		return underlying
	}
}

func (h *HeartbeatMetricsHandler) Gauge(name string) Gauge {
	underlying := h.underlying.Gauge(name)

	switch name {
	case StickyCacheSize:
		return &capturingGauge{
			underlying: underlying,
			value:      h.stickyCacheSize,
		}
	case WorkerTaskSlotsAvailable:
		var valuePtr *atomic.Uint64
		switch h.workerType {
		case "WorkflowWorker":
			valuePtr = h.workflowSlotsAvailable
		case "ActivityWorker":
			valuePtr = h.activitySlotsAvailable
		case "LocalActivityWorker":
			valuePtr = h.localActivitySlotsAvailable
		case "NexusWorker":
			valuePtr = h.nexusSlotsAvailable
		}
		if valuePtr != nil {
			return &capturingGauge{
				underlying: underlying,
				value:      valuePtr,
			}
		}
	case WorkerTaskSlotsUsed:
		var valuePtr *atomic.Uint64
		switch h.workerType {
		case "WorkflowWorker":
			valuePtr = h.workflowSlotsUsed
		case "ActivityWorker":
			valuePtr = h.activitySlotsUsed
		case "LocalActivityWorker":
			valuePtr = h.localActivitySlotsUsed
		case "NexusWorker":
			valuePtr = h.nexusSlotsUsed
		}
		if valuePtr != nil {
			return &capturingGauge{
				underlying: underlying,
				value:      valuePtr,
			}
		}
	case NumPoller:
		var valuePtr *atomic.Uint64
		switch h.pollerType {
		case PollerTypeWorkflowTask:
			valuePtr = h.workflowPollerCount
		case PollerTypeWorkflowStickyTask:
			valuePtr = h.workflowStickyPollerCount
		case PollerTypeActivityTask:
			valuePtr = h.activityPollerCount
		case PollerTypeNexusTask:
			valuePtr = h.nexusPollerCount
		}
		if valuePtr != nil {
			return &capturingGauge{
				underlying: underlying,
				value:      valuePtr,
			}
		}
	}

	return underlying
}

func (h *HeartbeatMetricsHandler) Timer(name string) Timer {
	underlying := h.underlying.Timer(name)

	// Capture execution latency timers to count processed tasks
	switch name {
	case WorkflowTaskExecutionLatency:
		return &capturingTimer{
			underlying: underlying,
			counter:    h.workflowTasksProcessed,
		}
	case ActivityExecutionLatency:
		return &capturingTimer{
			underlying: underlying,
			counter:    h.activityTasksProcessed,
		}
	case LocalActivityExecutionLatency:
		return &capturingTimer{
			underlying: underlying,
			counter:    h.localActivityTasksProcessed,
		}
	case NexusTaskExecutionLatency:
		return &capturingTimer{
			underlying: underlying,
			counter:    h.nexusTasksProcessed,
		}
	}

	return underlying
}

// GetStickyCacheHit returns the total number of sticky cache hits.
func (h *HeartbeatMetricsHandler) GetStickyCacheHit() int32 {
	return int32(h.stickyCacheHit.Load())
}

// GetStickyCacheMiss returns the total number of sticky cache misses.
func (h *HeartbeatMetricsHandler) GetStickyCacheMiss() int32 {
	return int32(h.stickyCacheMiss.Load())
}

// GetStickyCacheSize returns the current sticky cache size.
func (h *HeartbeatMetricsHandler) GetStickyCacheSize() int32 {
	return int32(h.stickyCacheSize.Load())
}

// GetWorkflowTaskFailures returns the total number of workflow task failures.
func (h *HeartbeatMetricsHandler) GetWorkflowTaskFailures() int64 {
	return int64(h.workflowTaskFailures.Load())
}

// GetActivityTaskFailures returns the total number of activity task failures.
func (h *HeartbeatMetricsHandler) GetActivityTaskFailures() int64 {
	return int64(h.activityTaskFailures.Load())
}

// GetLocalActivityTaskFailures returns the total number of local activity task failures.
func (h *HeartbeatMetricsHandler) GetLocalActivityTaskFailures() int64 {
	return int64(h.localActivityTaskFailures.Load())
}

// GetNexusTaskFailures returns the total number of nexus task failures.
func (h *HeartbeatMetricsHandler) GetNexusTaskFailures() int64 {
	return int64(h.nexusTaskFailures.Load())
}

// GetWorkflowSlotsAvailable returns the current workflow slots available.
func (h *HeartbeatMetricsHandler) GetWorkflowSlotsAvailable() int32 {
	return int32(h.workflowSlotsAvailable.Load())
}

// GetWorkflowSlotsUsed returns the current workflow slots used.
func (h *HeartbeatMetricsHandler) GetWorkflowSlotsUsed() int32 {
	return int32(h.workflowSlotsUsed.Load())
}

// GetActivitySlotsAvailable returns the current activity slots available.
func (h *HeartbeatMetricsHandler) GetActivitySlotsAvailable() int32 {
	return int32(h.activitySlotsAvailable.Load())
}

// GetActivitySlotsUsed returns the current activity slots used.
func (h *HeartbeatMetricsHandler) GetActivitySlotsUsed() int32 {
	return int32(h.activitySlotsUsed.Load())
}

// GetLocalActivitySlotsAvailable returns the current local activity slots available.
func (h *HeartbeatMetricsHandler) GetLocalActivitySlotsAvailable() int32 {
	return int32(h.localActivitySlotsAvailable.Load())
}

// GetLocalActivitySlotsUsed returns the current local activity slots used.
func (h *HeartbeatMetricsHandler) GetLocalActivitySlotsUsed() int32 {
	return int32(h.localActivitySlotsUsed.Load())
}

// GetNexusSlotsAvailable returns the current nexus slots available.
func (h *HeartbeatMetricsHandler) GetNexusSlotsAvailable() int32 {
	return int32(h.nexusSlotsAvailable.Load())
}

// GetNexusSlotsUsed returns the current nexus slots used.
func (h *HeartbeatMetricsHandler) GetNexusSlotsUsed() int32 {
	return int32(h.nexusSlotsUsed.Load())
}

// GetWorkflowTasksProcessed returns the total number of workflow tasks processed.
func (h *HeartbeatMetricsHandler) GetWorkflowTasksProcessed() int64 {
	return int64(h.workflowTasksProcessed.Load())
}

// GetActivityTasksProcessed returns the total number of activity tasks processed.
func (h *HeartbeatMetricsHandler) GetActivityTasksProcessed() int64 {
	return int64(h.activityTasksProcessed.Load())
}

// GetLocalActivityTasksProcessed returns the total number of local activity tasks processed.
func (h *HeartbeatMetricsHandler) GetLocalActivityTasksProcessed() int64 {
	return int64(h.localActivityTasksProcessed.Load())
}

// GetNexusTasksProcessed returns the total number of nexus tasks processed.
func (h *HeartbeatMetricsHandler) GetNexusTasksProcessed() int64 {
	return int64(h.nexusTasksProcessed.Load())
}

// GetWorkflowPollerCount returns the current number of workflow task pollers.
func (h *HeartbeatMetricsHandler) GetWorkflowPollerCount() int32 {
	return int32(h.workflowPollerCount.Load())
}

// GetWorkflowStickyPollerCount returns the current number of workflow sticky task pollers.
func (h *HeartbeatMetricsHandler) GetWorkflowStickyPollerCount() int32 {
	return int32(h.workflowStickyPollerCount.Load())
}

// GetActivityPollerCount returns the current number of activity task pollers.
func (h *HeartbeatMetricsHandler) GetActivityPollerCount() int32 {
	return int32(h.activityPollerCount.Load())
}

// GetNexusPollerCount returns the current number of nexus task pollers.
func (h *HeartbeatMetricsHandler) GetNexusPollerCount() int32 {
	return int32(h.nexusPollerCount.Load())
}

// RecordWorkflowPollSuccess records a successful workflow task poll.
func (h *HeartbeatMetricsHandler) RecordWorkflowPollSuccess() {
	h.workflowLastPoll.Store(time.Now().UnixNano())
}

// RecordWorkflowStickyPollSuccess records a successful workflow sticky task poll.
func (h *HeartbeatMetricsHandler) RecordWorkflowStickyPollSuccess() {
	h.workflowStickyLastPoll.Store(time.Now().UnixNano())
}

// RecordActivityPollSuccess records a successful activity task poll.
func (h *HeartbeatMetricsHandler) RecordActivityPollSuccess() {
	h.activityLastPoll.Store(time.Now().UnixNano())
}

// RecordNexusPollSuccess records a successful nexus task poll.
func (h *HeartbeatMetricsHandler) RecordNexusPollSuccess() {
	h.nexusLastPoll.Store(time.Now().UnixNano())
}

// GetWorkflowLastPollTime returns the last successful workflow task poll time.
func (h *HeartbeatMetricsHandler) GetWorkflowLastPollTime() time.Time {
	nanos := h.workflowLastPoll.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// GetWorkflowStickyLastPollTime returns the last successful workflow sticky task poll time.
func (h *HeartbeatMetricsHandler) GetWorkflowStickyLastPollTime() time.Time {
	nanos := h.workflowStickyLastPoll.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// GetActivityLastPollTime returns the last successful activity task poll time.
func (h *HeartbeatMetricsHandler) GetActivityLastPollTime() time.Time {
	nanos := h.activityLastPoll.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// GetNexusLastPollTime returns the last successful nexus task poll time.
func (h *HeartbeatMetricsHandler) GetNexusLastPollTime() time.Time {
	nanos := h.nexusLastPoll.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
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
func RecordPollSuccess(h Handler, pollerType string) {
	recorder, ok := h.(PollSuccessRecorder)
	if !ok {
		return
	}
	switch pollerType {
	case PollerTypeWorkflowTask:
		recorder.RecordWorkflowPollSuccess()
	case PollerTypeWorkflowStickyTask:
		recorder.RecordWorkflowStickyPollSuccess()
	case PollerTypeActivityTask:
		recorder.RecordActivityPollSuccess()
	case PollerTypeNexusTask:
		recorder.RecordNexusPollSuccess()
	}
}

// capturingCounter wraps a counter and captures its value in memory for heartbeat reporting.
type capturingCounter struct {
	underlying Counter
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
	underlying Gauge
	value      *atomic.Uint64
}

func (g *capturingGauge) Update(f float64) {
	g.underlying.Update(f)
	g.value.Store(uint64(f))
}

// capturingTimer wraps a timer and increments a counter each time Record is called.
type capturingTimer struct {
	underlying Timer
	counter    *atomic.Uint64
}

func (t *capturingTimer) Record(d time.Duration) {
	t.underlying.Record(d)
	t.counter.Add(1)
}
