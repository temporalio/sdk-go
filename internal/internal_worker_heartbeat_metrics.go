package internal

import (
	"sync/atomic"
	"time"

	workerpb "go.temporal.io/api/worker/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/internal/common/metrics"
)

// Metrics we capture for heartbeat reporting.
var (
	capturedCounters = map[string]bool{
		metrics.StickyCacheHit:                      true,
		metrics.StickyCacheMiss:                     true,
		metrics.WorkflowTaskExecutionFailureCounter: true,
		metrics.ActivityExecutionFailedCounter:      true,
		metrics.LocalActivityExecutionFailedCounter: true,
		metrics.NexusTaskExecutionFailedCounter:     true,
	}

	// Timer recordings are counted (not their latencies) to track tasks processed.
	capturedTimers = map[string]bool{
		metrics.WorkflowTaskExecutionLatency:  true,
		metrics.ActivityExecutionLatency:      true,
		metrics.LocalActivityExecutionLatency: true,
		metrics.NexusTaskExecutionLatency:     true,
	}
)

// heartbeatMetricsHandler wraps a metrics handler and captures specific metrics
// in memory for worker heartbeats.
type heartbeatMetricsHandler struct {
	underlying metrics.Handler
	workerType string
	pollerType string

	// All instances share the same underlying map (set on creation, never replaced).
	// Keys are metric names, or "metricName:workerType" / "metricName:pollerType" for typed metrics.
	metrics map[string]*atomic.Int64
}

// newHeartbeatMetricsHandler creates a new handler that captures specific metrics
// for worker heartbeats while passing all metrics to the underlying handler.
func newHeartbeatMetricsHandler(underlying metrics.Handler) *heartbeatMetricsHandler {
	return &heartbeatMetricsHandler{
		underlying: underlying,
		metrics:    make(map[string]*atomic.Int64),
	}
}

// forWorker creates a new handler that captures metrics specific to a worker type, for worker heartbeating.
// This should be called explicitly before calling WithTags on the returned handler.
func (h *heartbeatMetricsHandler) forWorker(workerType string) metrics.Handler {
	cpy := *h
	cpy.workerType = workerType
	return &cpy
}

// forPoller creates a new handler that captures metrics specific to a poller type, for worker heartbeating.
// This should be called explicitly before calling WithTags on the returned handler.
func (h *heartbeatMetricsHandler) forPoller(pollerType string) metrics.Handler {
	cpy := *h
	cpy.pollerType = pollerType
	return &cpy
}

func (h *heartbeatMetricsHandler) WithTags(tags map[string]string) metrics.Handler {
	cpy := *h
	cpy.underlying = h.underlying.WithTags(tags)
	return &cpy
}

func (h *heartbeatMetricsHandler) Counter(name string) metrics.Counter {
	underlying := h.underlying.Counter(name)
	if capturedCounters[name] {
		return &capturingCounter{
			underlying: underlying,
			value:      h.getOrCreate(name),
		}
	}
	return underlying
}

func (h *heartbeatMetricsHandler) Gauge(name string) metrics.Gauge {
	underlying := h.underlying.Gauge(name)

	switch name {
	case metrics.StickyCacheSize:
		return &capturingGauge{
			underlying: underlying,
			value:      h.getOrCreate(name),
		}
	case metrics.WorkerTaskSlotsAvailable, metrics.WorkerTaskSlotsUsed:
		if h.workerType != "" {
			return &capturingGauge{
				underlying: underlying,
				value:      h.getOrCreate(name + ":" + h.workerType),
			}
		}
	case metrics.NumPoller:
		if h.pollerType != "" {
			return &capturingGauge{
				underlying: underlying,
				value:      h.getOrCreate(name + ":" + h.pollerType),
			}
		}
	}

	return underlying
}

func (h *heartbeatMetricsHandler) Timer(name string) metrics.Timer {
	underlying := h.underlying.Timer(name)
	if capturedTimers[name] {
		return &capturingTimer{
			underlying: underlying,
			counter:    h.getOrCreate(name),
		}
	}
	return underlying
}

func (h *heartbeatMetricsHandler) getOrCreate(key string) *atomic.Int64 {
	if v, ok := h.metrics[key]; ok {
		return v
	}
	v := new(atomic.Int64)
	h.metrics[key] = v
	return v
}

func (h *heartbeatMetricsHandler) get(key string) int64 {
	if v, ok := h.metrics[key]; ok {
		return v.Load()
	}
	return 0
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

	// For delta calculations between heartbeats (mutated by PopulateHeartbeat).
	PrevWorkflowProcessed      *int64
	PrevWorkflowFailed         *int64
	PrevActivityProcessed      *int64
	PrevActivityFailed         *int64
	PrevLocalActivityProcessed *int64
	PrevLocalActivityFailed    *int64
	PrevNexusProcessed         *int64
	PrevNexusFailed            *int64
}

// PopulateHeartbeat fills in the metrics-related fields of the WorkerHeartbeat proto.
func (h *heartbeatMetricsHandler) PopulateHeartbeat(hb *workerpb.WorkerHeartbeat, opts *PopulateHeartbeatOptions) {
	hb.TotalStickyCacheHit = int32(h.get(metrics.StickyCacheHit))
	hb.TotalStickyCacheMiss = int32(h.get(metrics.StickyCacheMiss))
	hb.CurrentStickyCacheSize = int32(h.get(metrics.StickyCacheSize))

	if opts.WorkflowSlotSupplierKind != "" {
		hb.WorkflowTaskSlotsInfo = buildSlotsInfo(
			opts.WorkflowSlotSupplierKind,
			int32(h.get(metrics.WorkerTaskSlotsAvailable+":"+"WorkflowWorker")),
			int32(h.get(metrics.WorkerTaskSlotsUsed+":"+"WorkflowWorker")),
			h.get(metrics.WorkflowTaskExecutionLatency),
			h.get(metrics.WorkflowTaskExecutionFailureCounter),
			opts.PrevWorkflowProcessed,
			opts.PrevWorkflowFailed,
		)
	}

	if opts.ActivitySlotSupplierKind != "" {
		hb.ActivityTaskSlotsInfo = buildSlotsInfo(
			opts.ActivitySlotSupplierKind,
			int32(h.get(metrics.WorkerTaskSlotsAvailable+":"+"ActivityWorker")),
			int32(h.get(metrics.WorkerTaskSlotsUsed+":"+"ActivityWorker")),
			h.get(metrics.ActivityExecutionLatency),
			h.get(metrics.ActivityExecutionFailedCounter),
			opts.PrevActivityProcessed,
			opts.PrevActivityFailed,
		)
	}

	if opts.LocalActivitySlotSupplierKind != "" {
		hb.LocalActivitySlotsInfo = buildSlotsInfo(
			opts.LocalActivitySlotSupplierKind,
			int32(h.get(metrics.WorkerTaskSlotsAvailable+":"+"LocalActivityWorker")),
			int32(h.get(metrics.WorkerTaskSlotsUsed+":"+"LocalActivityWorker")),
			h.get(metrics.LocalActivityExecutionLatency),
			h.get(metrics.LocalActivityExecutionFailedCounter),
			opts.PrevLocalActivityProcessed,
			opts.PrevLocalActivityFailed,
		)
	}

	if opts.NexusSlotSupplierKind != "" {
		hb.NexusTaskSlotsInfo = buildSlotsInfo(
			opts.NexusSlotSupplierKind,
			int32(h.get(metrics.WorkerTaskSlotsAvailable+":"+"NexusWorker")),
			int32(h.get(metrics.WorkerTaskSlotsUsed+":"+"NexusWorker")),
			h.get(metrics.NexusTaskExecutionLatency),
			h.get(metrics.NexusTaskExecutionFailedCounter),
			opts.PrevNexusProcessed,
			opts.PrevNexusFailed,
		)
	}

	hb.WorkflowPollerInfo = buildPollerInfo(
		int32(h.get(metrics.NumPoller+":"+metrics.PollerTypeWorkflowTask)),
		h.getLastPollTime(metrics.PollerTypeWorkflowTask),
		opts.WorkflowPollerBehavior,
	)
	hb.WorkflowStickyPollerInfo = buildPollerInfo(
		int32(h.get(metrics.NumPoller+":"+metrics.PollerTypeWorkflowStickyTask)),
		h.getLastPollTime(metrics.PollerTypeWorkflowStickyTask),
		opts.WorkflowPollerBehavior,
	)
	hb.ActivityPollerInfo = buildPollerInfo(
		int32(h.get(metrics.NumPoller+":"+metrics.PollerTypeActivityTask)),
		h.getLastPollTime(metrics.PollerTypeActivityTask),
		opts.ActivityPollerBehavior,
	)
	hb.NexusPollerInfo = buildPollerInfo(
		int32(h.get(metrics.NumPoller+":"+metrics.PollerTypeNexusTask)),
		h.getLastPollTime(metrics.PollerTypeNexusTask),
		opts.NexusPollerBehavior,
	)
}

func (h *heartbeatMetricsHandler) getLastPollTime(pollerType string) time.Time {
	nanos := h.get(pollerType)
	if nanos != 0 {
		return time.Unix(0, nanos)
	}
	return time.Time{}
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

// recordPollSuccessIfHeartbeat records a successful poll time if the handler is a *heartbeatMetricsHandler.
func recordPollSuccessIfHeartbeat(h metrics.Handler, pollerType string) {
	if hm, ok := h.(*heartbeatMetricsHandler); ok {
		hm.getOrCreate(pollerType).Store(time.Now().UnixNano())
	}
}

// capturingCounter wraps a counter and captures its value in memory.
type capturingCounter struct {
	underlying metrics.Counter
	value      *atomic.Int64
}

func (c *capturingCounter) Inc(delta int64) {
	c.underlying.Inc(delta)
	if delta > 0 {
		c.value.Add(delta)
	}
}

// capturingGauge wraps a gauge and captures its value in memory.
type capturingGauge struct {
	underlying metrics.Gauge
	value      *atomic.Int64
}

func (g *capturingGauge) Update(f float64) {
	g.underlying.Update(f)
	g.value.Store(int64(f))
}

// capturingTimer wraps a timer and increments a counter each time Record is called.
type capturingTimer struct {
	underlying metrics.Timer
	counter    *atomic.Int64
}

func (t *capturingTimer) Record(d time.Duration) {
	t.underlying.Record(d)
	t.counter.Add(1)
}
