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
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.einride.tech/pid"
	"go.temporal.io/sdk/worker"
)

// CreateResourceBasedTuner creates a WorkerTuner that dynamically adjusts the number of slots based
// on system resources. Specify the target CPU and memory usage as a value between 0 and 1.
//
// WARNING: Resource based tuning is currently experimental.
func CreateResourceBasedTuner(targetCpu, targetMem float64) (worker.WorkerTuner, error) {
	options := DefaultResourceControllerOptions()
	options.MemTargetPercent = targetMem
	options.CpuTargetPercent = targetCpu
	controller := NewResourceControllerWithInfoSupplier(options, &psUtilSystemInfoSupplier{})
	wfSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultWorkflowResourceBasedSlotSupplierOptions()}
	actSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
	laSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
	compositeTuner := worker.CreateCompositeTuner(wfSS, actSS, laSS)
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
		MaxSlots:     1000,
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
) *ResourceBasedSlotSupplier {
	return &ResourceBasedSlotSupplier{controller: controller, options: options}
}

func (r *ResourceBasedSlotSupplier) ReserveSlot(ctx context.Context, reserveCtx worker.SlotReserveContext) (*worker.SlotPermit, error) {
	for {
		if reserveCtx.NumIssuedSlots() < r.options.MinSlots {
			return &worker.SlotPermit{}, nil
		}
		r.lastIssuedMu.Lock()
		mustWaitFor := r.options.RampThrottle - time.Since(r.lastSlotIssuedAt)
		r.lastIssuedMu.Unlock()
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
	r.lastIssuedMu.Lock()
	defer r.lastIssuedMu.Unlock()

	numIssued := reserveCtx.NumIssuedSlots()
	if numIssued < r.options.MinSlots || (numIssued < r.options.MaxSlots &&
		time.Since(r.lastSlotIssuedAt) > r.options.RampThrottle) {
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

// SystemInfoSupplier implementations provide information about system resources.
//
// WARNING: Resource based tuning is currently experimental.
type SystemInfoSupplier interface {
	// GetMemoryUsage returns the current system memory usage as a fraction of total memory between
	// 0 and 1.
	GetMemoryUsage() (float64, error)
	// GetCpuUsage returns the current system CPU usage as a fraction of total CPU usage between 0
	// and 1.
	GetCpuUsage() (float64, error)
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
	return NewResourceControllerWithInfoSupplier(options, &psUtilSystemInfoSupplier{})
}

// NewResourceControllerWithInfoSupplier creates a new ResourceController with the provided options
// and system information supplier. Only use this if you need to override the default system info
// supplier.
//
// WARNING: Resource based tuning is currently experimental.
func NewResourceControllerWithInfoSupplier(
	options ResourceControllerOptions,
	infoSupplier SystemInfoSupplier,
) *ResourceController {
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

func (rc *ResourceController) pidDecision() (bool, error) {
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
