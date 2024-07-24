package resourcetuner

import (
	"context"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.einride.tech/pid"
	"go.temporal.io/sdk/worker"
)

func CreateResourceBasedTuner(targetCpu, targetMem float64) (worker.WorkerTuner, error) {
	options := DefaultResourceControllerOptions()
	options.memTargetPercent = targetMem
	options.cpuTargetPercent = targetCpu
	controller := newResourceController(options, &psUtilSystemInfoSupplier{})
	// TODO: configurable
	wfSS := &ResourceBasedSlotSupplier{controller: controller, minSlots: 5, maxSlots: 1000}
	actSS := &ResourceBasedSlotSupplier{controller: controller, minSlots: 1, maxSlots: 1000}
	laSS := &ResourceBasedSlotSupplier{controller: controller, minSlots: 1, maxSlots: 1000}
	compositeTuner := worker.CreateCompositeTuner(wfSS, actSS, laSS)
	return compositeTuner, nil
}

type ResourceBasedSlotSupplier struct {
	controller       *resourceController
	minSlots         int
	maxSlots         int
	rampThrottle     time.Duration
	lastSlotIssuedAt time.Time
}

func (r *ResourceBasedSlotSupplier) ReserveSlot(ctx context.Context, reserveCtx worker.SlotReserveContext) (*worker.SlotPermit, error) {
	for {
		if reserveCtx.NumIssuedSlots() < r.minSlots {
			return &worker.SlotPermit{}, nil
		}
		mustWaitFor := r.rampThrottle - time.Since(r.lastSlotIssuedAt)
		if mustWaitFor > 0 {
			select {
			case <-time.After(mustWaitFor):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		maybePermit := r.TryReserveSlot(reserveCtx)
		if maybePermit != nil {
			return maybePermit, nil
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (r *ResourceBasedSlotSupplier) TryReserveSlot(reserveCtx worker.SlotReserveContext) *worker.SlotPermit {
	numIssued := reserveCtx.NumIssuedSlots()
	if numIssued < r.minSlots || (numIssued < r.maxSlots &&
		time.Since(r.lastSlotIssuedAt) > r.rampThrottle) {
		decision, err := r.controller.pidDecision()
		if err != nil {
			// TODO: log
			return nil
		}
		if decision {
			r.lastSlotIssuedAt = time.Now()
			return &worker.SlotPermit{}
		}
	}
	return nil
}

func (r *ResourceBasedSlotSupplier) MarkSlotUsed() {}
func (r *ResourceBasedSlotSupplier) ReleaseSlot()  {}
func (r *ResourceBasedSlotSupplier) MaximumSlots() int {
	return 0
}

type SystemInfoSupplier interface {
	// GetMemoryUsage returns the current system memory usage as a fraction of total memory between
	// 0 and 1.
	GetMemoryUsage() (float64, error)
	// GetCpuUsage returns the current system CPU usage as a fraction of total CPU usage between 0
	// and 1.
	GetCpuUsage() (float64, error)
}

type ResourceControllerOptions struct {
	memTargetPercent float64
	cpuTargetPercent float64

	memOutputThreshold float64
	cpuOutputThreshold float64

	MemPGain float64
	MemIGain float64
	MemDGain float64
	CpuPGain float64
	CpuIGain float64
	CpuDGain float64
}

func DefaultResourceControllerOptions() ResourceControllerOptions {
	return ResourceControllerOptions{
		memTargetPercent:   0.8,
		cpuTargetPercent:   0.9,
		memOutputThreshold: 0.25,
		cpuOutputThreshold: 0.05,
		MemPGain:           5,
		MemIGain:           0,
		MemDGain:           1,
		CpuPGain:           5,
		CpuIGain:           0,
		CpuDGain:           1,
	}
}

type resourceController struct {
	options ResourceControllerOptions

	mu           sync.Mutex
	infoSupplier SystemInfoSupplier
	lastRefresh  time.Time
	memPid       *pid.Controller
	cpuPid       *pid.Controller
}

func newResourceController(
	options ResourceControllerOptions,
	infoSupplier SystemInfoSupplier,
) *resourceController {
	return &resourceController{
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

func (rc *resourceController) pidDecision() (bool, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	memUsage, err := rc.infoSupplier.GetMemoryUsage()
	if err != nil {
		return false, err
	}
	cpuUsage, err := rc.infoSupplier.GetCpuUsage()
	if err != nil {
		return false, err
	}
	rc.memPid.Update(pid.ControllerInput{
		ReferenceSignal:  rc.options.memTargetPercent,
		ActualSignal:     memUsage,
		SamplingInterval: time.Since(rc.lastRefresh),
	})
	rc.cpuPid.Update(pid.ControllerInput{
		ReferenceSignal:  rc.options.cpuTargetPercent,
		ActualSignal:     cpuUsage,
		SamplingInterval: time.Since(rc.lastRefresh),
	})
	rc.lastRefresh = time.Now()

	return rc.memPid.State.ControlSignal > rc.options.memOutputThreshold &&
		rc.cpuPid.State.ControlSignal > rc.options.cpuOutputThreshold, nil
}

type psUtilSystemInfoSupplier struct {
	mu           sync.Mutex
	lastMemStat  *mem.VirtualMemoryStat
	lastCpuUsage float64
	lastRefresh  time.Time
}

func (p *psUtilSystemInfoSupplier) GetMemoryUsage() (float64, error) {
	if err := p.maybeRefresh(); err != nil {
		return 0, err
	}
	return p.lastMemStat.UsedPercent / 100, nil
}

func (p *psUtilSystemInfoSupplier) GetCpuUsage() (float64, error) {
	if err := p.maybeRefresh(); err != nil {
		return 0, err
	}
	return p.lastCpuUsage, nil
}

func (p *psUtilSystemInfoSupplier) maybeRefresh() error {
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Double check refresh is still needed
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	memStat, err := mem.VirtualMemory()
	if err != nil {
		return err
	}
	cpuUsage, err := cpu.Percent(0, false)
	if err != nil {
		return err
	}
	p.lastMemStat = memStat
	p.lastCpuUsage = cpuUsage[0]
	return nil
}
