// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package resourcetuner

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.einride.tech/pid"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
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

// NewResourceBasedTuner creates a WorkerTuner that dynamically adjusts the number of slots based
// on system resources. Specify the target CPU and memory usage as a value between 0 and 1.
//
// WARNING: Resource based tuning is currently experimental.
func NewResourceBasedTuner(opts ResourceBasedTunerOptions) (worker.WorkerTuner, error) {
	options := DefaultResourceControllerOptions()
	options.MemTargetPercent = opts.TargetMem
	options.CpuTargetPercent = opts.TargetCpu
	controller := NewResourceController(options)
	wfSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultWorkflowResourceBasedSlotSupplierOptions()}
	if opts.WorkflowRampThrottle != 0 {
		wfSS.options.RampThrottle = opts.WorkflowRampThrottle
	}
	actSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
	if opts.ActivityRampThrottle != 0 {
		actSS.options.RampThrottle = opts.ActivityRampThrottle
	}
	laSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
	if opts.ActivityRampThrottle != 0 {
		laSS.options.RampThrottle = opts.ActivityRampThrottle
	}
	nexusSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultWorkflowResourceBasedSlotSupplierOptions()}
	compositeTuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier:      wfSS,
		ActivitySlotSupplier:      actSS,
		LocalActivitySlotSupplier: laSS,
		NexusSlotSupplier:         nexusSS,
	})
	if err != nil {
		return nil, err
	}
	return compositeTuner, nil
}

// ResourceBasedSlotSupplierOptions configures a particular ResourceBasedSlotSupplier.
//
// WARNING: Resource based tuning is currently experimental.
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

func defaultWorkflowResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return ResourceBasedSlotSupplierOptions{
		MinSlots:     5,
		MaxSlots:     1000,
		RampThrottle: 0 * time.Second,
	}
}
func defaultActivityResourceBasedSlotSupplierOptions() ResourceBasedSlotSupplierOptions {
	return ResourceBasedSlotSupplierOptions{
		MinSlots:     1,
		MaxSlots:     10_000,
		RampThrottle: 50 * time.Millisecond,
	}
}

// ResourceBasedSlotSupplier is a worker.SlotSupplier that issues slots based on system resource
// usage.
//
// WARNING: Resource based tuning is currently experimental.
type ResourceBasedSlotSupplier struct {
	controller *ResourceController
	options    ResourceBasedSlotSupplierOptions

	lastIssuedMu     sync.Mutex
	lastSlotIssuedAt time.Time
}

// NewResourceBasedSlotSupplier creates a ResourceBasedSlotSupplier given the provided
// ResourceController and ResourceBasedSlotSupplierOptions. All ResourceBasedSlotSupplier instances
// must use the same ResourceController.
//
// WARNING: Resource based tuning is currently experimental.
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
		decision, err := r.controller.pidDecision(info.Logger())
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

// SystemInfoSupplier implementations provide information about system resources.
//
// WARNING: Resource based tuning is currently experimental.
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
//
// WARNING: Resource based tuning is currently experimental.
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
//
// WARNING: Resource based tuning is currently experimental.
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
//
// WARNING: Resource based tuning is currently experimental.
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
//
// WARNING: Resource based tuning is currently experimental.
func NewResourceController(options ResourceControllerOptions) *ResourceController {
	var infoSupplier SystemInfoSupplier
	if options.InfoSupplier == nil {
		infoSupplier = &psUtilSystemInfoSupplier{
			cGroupInfo: newCGroupInfo(),
		}
	} else {
		infoSupplier = options.InfoSupplier
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

func (rc *ResourceController) pidDecision(logger log.Logger) (bool, error) {
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

type psUtilSystemInfoSupplier struct {
	logger      log.Logger
	mu          sync.Mutex
	lastRefresh time.Time

	lastMemStat  *mem.VirtualMemoryStat
	lastCpuUsage float64

	stopTryingToGetCGroupInfo bool
	cGroupInfo                cGroupInfo
}

type cGroupInfo interface {
	// Update requests an update of the cgroup stats. This is a no-op if not in a cgroup. Returns
	// true if cgroup stats should continue to be updated, false if not in a cgroup or the returned
	// error is considered unrecoverable.
	Update() (bool, error)
	// GetLastMemUsage returns last known memory usage as a fraction of the cgroup limit. 0 if not
	// in a cgroup or limit is not set.
	GetLastMemUsage() float64
	// GetLastCPUUsage returns last known CPU usage as a fraction of the cgroup limit. 0 if not in a
	// cgroup or limit is not set.
	GetLastCPUUsage() float64
}

func (p *psUtilSystemInfoSupplier) GetMemoryUsage(infoContext *SystemInfoContext) (float64, error) {
	if err := p.maybeRefresh(infoContext); err != nil {
		return 0, err
	}
	lastCGroupMem := p.cGroupInfo.GetLastMemUsage()
	if lastCGroupMem != 0 {
		return lastCGroupMem, nil
	}
	return p.lastMemStat.UsedPercent / 100, nil
}

func (p *psUtilSystemInfoSupplier) GetCpuUsage(infoContext *SystemInfoContext) (float64, error) {
	if err := p.maybeRefresh(infoContext); err != nil {
		return 0, err
	}

	lastCGroupCPU := p.cGroupInfo.GetLastCPUUsage()
	if lastCGroupCPU != 0 {
		return lastCGroupCPU, nil
	}
	return p.lastCpuUsage / 100, nil
}

func (p *psUtilSystemInfoSupplier) maybeRefresh(infoContext *SystemInfoContext) error {
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Double check refresh is still needed
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelFn()
	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return err
	}
	cpuUsage, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return err
	}

	p.lastMemStat = memStat
	p.lastCpuUsage = cpuUsage[0]

	if runtime.GOOS == "linux" && !p.stopTryingToGetCGroupInfo {
		continueUpdates, err := p.cGroupInfo.Update()
		if err != nil {
			infoContext.Logger.Warn("Failed to get cgroup stats", "error", err)
		}
		p.stopTryingToGetCGroupInfo = !continueUpdates
	}

	p.lastRefresh = time.Now()
	return nil
}
