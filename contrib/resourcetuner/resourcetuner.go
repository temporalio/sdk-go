package resourcetuner

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.einride.tech/pid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/worker/hostmetrics"
)

// Metric names emitted by the resource-based tuner
const (
	resourceSlotsCPUUsage = "temporal_resource_slots_cpu_usage"
	resourceSlotsMemUsage = "temporal_resource_slots_mem_usage"
)

type ResourceBasedTunerOptions struct {
	// TargetMem is the target overall system memory usage as value 0 and 1 that the controller will
	// attempt to maintain. Must be set nonzero.
	TargetMem float64
	// TargetCpu is the target overall system CPU usage as value 0 and 1 that the controller will
	// attempt to maintain. Must be set nonzero.
	TargetCpu float64
	// Passed to ResourceBasedSlotSupplierOptions.RampThrottle for activities.
	// If not set, the default value is 50ms.
	ActivityRampThrottle time.Duration
	// Passed to ResourceBasedSlotSupplierOptions.RampThrottle for workflows.
	// If not set, the default value is 0ms.
	WorkflowRampThrottle time.Duration
}

// resourceBasedTuner wraps a WorkerTuner and implements TunerHostMetricsProvider
// so the SDK can reuse metrics instead of collecting them twice.
type resourceBasedTuner struct {
	worker.WorkerTuner
	hostMetrics *hostmetrics.PSUtilSystemInfoSupplier
}

func (t *resourceBasedTuner) GetCpuUsage() (float64, error) {
	return t.hostMetrics.GetCpuUsage()
}

func (t *resourceBasedTuner) GetMemoryUsage() (float64, error) {
	return t.hostMetrics.GetMemoryUsage()
}

// NewResourceBasedTuner creates a WorkerTuner that dynamically adjusts the number of slots based
// on system resources. Specify the target CPU and memory usage as a value between 0 and 1.
func NewResourceBasedTuner(opts ResourceBasedTunerOptions) (worker.WorkerTuner, error) {
	hostMetrics := hostmetrics.NewPSUtilSystemInfoSupplier(nil)

	options := DefaultResourceControllerOptions()
	options.MemTargetPercent = opts.TargetMem
	options.CpuTargetPercent = opts.TargetCpu
	options.InfoSupplier = &hostMetricsInfoSupplier{provider: hostMetrics}
	controller := NewResourceController(options)
	wfSS := &ResourceBasedSlotSupplier{controller: controller,
		options: DefaultWorkflowResourceBasedSlotSupplierOptions()}
	if opts.WorkflowRampThrottle != 0 {
		wfSS.options.RampThrottle = opts.WorkflowRampThrottle
	}
	actSS := &ResourceBasedSlotSupplier{controller: controller,
		options: DefaultActivityResourceBasedSlotSupplierOptions()}
	if opts.ActivityRampThrottle != 0 {
		actSS.options.RampThrottle = opts.ActivityRampThrottle
	}
	laSS := &ResourceBasedSlotSupplier{controller: controller,
		options: DefaultActivityResourceBasedSlotSupplierOptions()}
	if opts.ActivityRampThrottle != 0 {
		laSS.options.RampThrottle = opts.ActivityRampThrottle
	}
	nexusSS := &ResourceBasedSlotSupplier{controller: controller,
		options: DefaultWorkflowResourceBasedSlotSupplierOptions()}
	sessSS := &ResourceBasedSlotSupplier{controller: controller,
		options: DefaultActivityResourceBasedSlotSupplierOptions()}
	compositeTuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier:        wfSS,
		ActivitySlotSupplier:        actSS,
		LocalActivitySlotSupplier:   laSS,
		NexusSlotSupplier:           nexusSS,
		SessionActivitySlotSupplier: sessSS,
	})
	if err != nil {
		return nil, err
	}
	return &resourceBasedTuner{
		WorkerTuner: compositeTuner,
		hostMetrics: hostMetrics,
	}, nil
}

// hostMetricsInfoSupplier adapts hostmetrics.PSUtilSystemInfoSupplier to SystemInfoSupplier
type hostMetricsInfoSupplier struct {
	provider *hostmetrics.PSUtilSystemInfoSupplier
}

func (s *hostMetricsInfoSupplier) GetMemoryUsage(ctx *SystemInfoContext) (float64, error) {
	return s.provider.GetMemoryUsageWithLogger(ctx.Logger)
}

func (s *hostMetricsInfoSupplier) GetCpuUsage(ctx *SystemInfoContext) (float64, error) {
	return s.provider.GetCpuUsageWithLogger(ctx.Logger)
}

// ResourceBasedSlotSupplierOptions configures a particular ResourceBasedSlotSupplier.
type ResourceBasedSlotSupplierOptions struct {
	// MinSlots is minimum number of slots that will be issued without any resource checks.
	MinSlots int
	// MaxSlots is the maximum number of slots that will ever be issued.
	MaxSlots int
	// RampThrottle is time to wait between slot issuance. This value matters (particularly for
	// activities) because how many resources a task will use cannot be determined ahead of time,
	// and thus the system should wait to see how much resources are used before issuing more slots.
	RampThrottle time.Duration
}

func DefaultWorkflowResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return ResourceBasedSlotSupplierOptions{
		MinSlots:     5,
		MaxSlots:     1000,
		RampThrottle: 0 * time.Second,
	}
}
func DefaultActivityResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return ResourceBasedSlotSupplierOptions{
		MinSlots:     1,
		MaxSlots:     10_000,
		RampThrottle: 50 * time.Millisecond,
	}
}

// ResourceBasedSlotSupplier is a worker.SlotSupplier that issues slots based on system resource
// usage.
type ResourceBasedSlotSupplier struct {
	controller *ResourceController
	options    ResourceBasedSlotSupplierOptions

	lastIssuedMu     sync.Mutex
	lastSlotIssuedAt time.Time
}

// NewResourceBasedSlotSupplier creates a ResourceBasedSlotSupplier given the provided
// ResourceController and ResourceBasedSlotSupplierOptions. All ResourceBasedSlotSupplier instances
// must use the same ResourceController.
func NewResourceBasedSlotSupplier(
	controller *ResourceController,
	options ResourceBasedSlotSupplierOptions,
) (*ResourceBasedSlotSupplier, error) {
	if options.MinSlots < 0 || options.MaxSlots < 0 || options.MinSlots > options.MaxSlots {
		return nil, errors.New("MinSlots and Max slots must be non-negative and MinSlots must be less than or equal to MaxSlots")
	}
	if options.RampThrottle < 0 {
		return nil, errors.New("RampThrottle must be non-negative")
	}
	return &ResourceBasedSlotSupplier{controller: controller, options: options}, nil
}

func (r *ResourceBasedSlotSupplier) ReserveSlot(ctx context.Context, info worker.SlotReservationInfo) (*worker.SlotPermit, error) {
	for {
		if info.NumIssuedSlots() < r.options.MinSlots {
			return &worker.SlotPermit{}, nil
		}
		if r.options.RampThrottle > 0 {
			r.lastIssuedMu.Lock()
			mustWaitFor := r.options.RampThrottle - time.Since(r.lastSlotIssuedAt)
			// Deal with last issued possibly being unset, or, on windows seemingly sometimes can
			// have zero values if called rapidly enough.
			if mustWaitFor > 0 {
				select {
				case <-time.After(mustWaitFor):
				case <-ctx.Done():
					r.lastIssuedMu.Unlock()
					return nil, ctx.Err()
				}
			}
			r.lastIssuedMu.Unlock()
		}

		maybePermit := r.TryReserveSlot(info)
		if maybePermit != nil {
			return maybePermit, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *ResourceBasedSlotSupplier) TryReserveSlot(info worker.SlotReservationInfo) *worker.SlotPermit {
	r.lastIssuedMu.Lock()
	defer r.lastIssuedMu.Unlock()

	numIssued := info.NumIssuedSlots()
	if numIssued < r.options.MinSlots || (numIssued < r.options.MaxSlots &&
		time.Since(r.lastSlotIssuedAt) > r.options.RampThrottle) {
		decision, err := r.controller.pidDecision(info.Logger(), info.MetricsHandler())
		if err != nil {
			info.Logger().Error("Error calculating resource usage", "error", err)
			return nil
		}
		if decision {
			r.lastSlotIssuedAt = time.Now()
			return &worker.SlotPermit{}
		}
	}
	return nil
}

func (r *ResourceBasedSlotSupplier) MarkSlotUsed(worker.SlotMarkUsedInfo) {}
func (r *ResourceBasedSlotSupplier) ReleaseSlot(worker.SlotReleaseInfo)   {}
func (r *ResourceBasedSlotSupplier) MaxSlots() int {
	return 0
}
func (r *ResourceBasedSlotSupplier) Kind() string {
	return "ResourceBased"
}

// SystemInfoSupplier implementations provide information about system resources.
type SystemInfoSupplier interface {
	// GetMemoryUsage returns the current system memory usage as a fraction of total memory between
	// 0 and 1.
	GetMemoryUsage(infoContext *SystemInfoContext) (float64, error)
	// GetCpuUsage returns the current system CPU usage as a fraction of total CPU usage between 0
	// and 1.
	GetCpuUsage(infoContext *SystemInfoContext) (float64, error)
}

type SystemInfoContext struct {
	Logger log.Logger
}

// ResourceControllerOptions contains configurable parameters for a ResourceController.
// It is recommended to use DefaultResourceControllerOptions to create a ResourceControllerOptions
// and only modify the mem/cpu target percent fields.
type ResourceControllerOptions struct {
	// MemTargetPercent is the target overall system memory usage as value 0 and 1 that the
	// controller will attempt to maintain.
	MemTargetPercent float64
	// CpuTargetPercent is the target overall system CPU usage as value 0 and 1 that the controller
	// will attempt to maintain.
	CpuTargetPercent float64
	// SystemInfoSupplier is the supplier that the controller will use to get system resources.
	// Leave this nil to use the default implementation.
	InfoSupplier SystemInfoSupplier

	MemOutputThreshold float64
	CpuOutputThreshold float64

	MemPGain float64
	MemIGain float64
	MemDGain float64
	CpuPGain float64
	CpuIGain float64
	CpuDGain float64
}

// DefaultResourceControllerOptions returns a ResourceControllerOptions with default values.
func DefaultResourceControllerOptions() ResourceControllerOptions {
	return ResourceControllerOptions{
		MemTargetPercent:   0.8,
		CpuTargetPercent:   0.9,
		MemOutputThreshold: 0.25,
		CpuOutputThreshold: 0.05,
		MemPGain:           5,
		MemIGain:           0,
		MemDGain:           1,
		CpuPGain:           5,
		CpuIGain:           0,
		CpuDGain:           1,
	}
}

// A ResourceController is used by ResourceBasedSlotSupplier to make decisions about whether slots
// should be issued based on system resource usage.
type ResourceController struct {
	options ResourceControllerOptions

	mu           sync.Mutex
	infoSupplier SystemInfoSupplier
	lastRefresh  time.Time
	memPid       *pid.Controller
	cpuPid       *pid.Controller
}

// NewResourceController creates a new ResourceController with the provided options.
// WARNING: It is important that you do not create multiple ResourceController instances. Since
// the controller looks at overall system resources, multiple instances with different configs can
// only conflict with one another.
func NewResourceController(options ResourceControllerOptions) *ResourceController {
	infoSupplier := options.InfoSupplier
	if infoSupplier == nil {
		infoSupplier = &hostMetricsInfoSupplier{
			provider: hostmetrics.NewPSUtilSystemInfoSupplier(nil),
		}
	}
	return &ResourceController{
		options:      options,
		infoSupplier: infoSupplier,
		memPid: &pid.Controller{
			Config: pid.ControllerConfig{
				ProportionalGain: options.MemPGain,
				IntegralGain:     options.MemIGain,
				DerivativeGain:   options.MemDGain,
			},
		},
		cpuPid: &pid.Controller{
			Config: pid.ControllerConfig{
				ProportionalGain: options.CpuPGain,
				IntegralGain:     options.CpuIGain,
				DerivativeGain:   options.CpuDGain,
			},
		},
	}
}

func (rc *ResourceController) pidDecision(logger log.Logger, metricsHandler client.MetricsHandler) (bool, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	memUsage, err := rc.infoSupplier.GetMemoryUsage(&SystemInfoContext{Logger: logger})
	if err != nil {
		return false, err
	}
	cpuUsage, err := rc.infoSupplier.GetCpuUsage(&SystemInfoContext{Logger: logger})
	if err != nil {
		return false, err
	}
	rc.publishResourceMetrics(metricsHandler, memUsage, cpuUsage)
	if memUsage >= rc.options.MemTargetPercent {
		// Never allow going over the memory target
		return false, nil
	}
	elapsedTime := time.Since(rc.lastRefresh)
	// This shouldn't be possible with real implementations, but if the elapsed time is 0 the
	// PID controller can produce NaNs.
	if elapsedTime <= 0 {
		elapsedTime = 1 * time.Millisecond
	}
	rc.memPid.Update(pid.ControllerInput{
		ReferenceSignal:  rc.options.MemTargetPercent,
		ActualSignal:     memUsage,
		SamplingInterval: elapsedTime,
	})
	rc.cpuPid.Update(pid.ControllerInput{
		ReferenceSignal:  rc.options.CpuTargetPercent,
		ActualSignal:     cpuUsage,
		SamplingInterval: elapsedTime,
	})
	rc.lastRefresh = time.Now()

	return rc.memPid.State.ControlSignal > rc.options.MemOutputThreshold &&
		rc.cpuPid.State.ControlSignal > rc.options.CpuOutputThreshold, nil
}

func (rc *ResourceController) publishResourceMetrics(metricsHandler client.MetricsHandler, memUsage, cpuUsage float64) {
	if metricsHandler == nil {
		return
	}
	metricsHandler.Gauge(resourceSlotsMemUsage).Update(memUsage * 100)
	metricsHandler.Gauge(resourceSlotsCPUUsage).Update(cpuUsage * 100)
}
