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
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.einride.tech/pid"
	"go.temporal.io/sdk/worker"
)

type ResourceBasedTunerOptions struct {
	TargetMem float64
	TargetCpu float64
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
	actSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
	laSS := &ResourceBasedSlotSupplier{controller: controller,
		options: defaultActivityResourceBasedSlotSupplierOptions()}
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
		decision, err := r.controller.pidDecision()
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
		infoSupplier = &Cgroup2SystemInfoSupplier{}
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
	if memUsage >= rc.options.MemTargetPercent {
		// Never allow going over the memory target
		return false, nil
	}
	//fmt.Printf("mem: %f, cpu: %f\n", memUsage, cpuUsage)
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
	return p.lastCpuUsage / 100, nil
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
	ctx, cancelFn := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancelFn()
	memStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		println("Refresh error: ", err)
		return err
	}
	cpuUsage, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		println("Refresh error: ", err)
		return err
	}
	p.lastMemStat = memStat
	p.lastCpuUsage = cpuUsage[0]
	p.lastRefresh = time.Now()
	return nil
}

// Cgroup2SystemInfoSupplier implements SystemInfoSupplier to gather resource info from cgroup2
type Cgroup2SystemInfoSupplier struct {
	mu              sync.Mutex
	lastCpuUsage    uint64
	lastRefresh     time.Time
	lastCpuTotalUse float64

	lastCpuUsageFraction float64
	lastMemUsageFraction float64
}

func (p *Cgroup2SystemInfoSupplier) maybeRefresh() error {
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Double check refresh is still needed
	if time.Since(p.lastRefresh) < 100*time.Millisecond {
		return nil
	}
	control, err := cgroup2.Load("/")
	if err != nil {
		return err
	}
	metrics, err := control.Stat()
	if err != nil {
		return err
	}
	p.lastMemUsageFraction = float64(metrics.Memory.Usage) / float64(metrics.Memory.UsageLimit)

	// Fetch the cgroup stats
	metrics, err = control.Stat()
	if err != nil {
		return err
	}

	// Read CPU quota and period from cpu.max
	cpuQuota, cpuPeriod, err := readCpuMax("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return err
	}

	// CPU usage calculation based on delta
	currentCpuUsage := metrics.CPU.UsageUsec
	now := time.Now()

	if p.lastCpuUsage == 0 || p.lastRefresh.IsZero() {
		p.lastCpuUsage = currentCpuUsage
		p.lastRefresh = now
		return nil
	}

	// Time passed between this and last check
	timeDelta := now.Sub(p.lastRefresh).Microseconds() // Convert to microseconds

	// Calculate CPU usage percentage based on the delta
	cpuUsageDelta := float64(currentCpuUsage - p.lastCpuUsage)
	var cpuUsage float64

	if cpuQuota > 0 {
		cpuUsage = cpuUsageDelta * float64(cpuPeriod) / float64(cpuQuota*timeDelta)
	}

	// Update for next call
	p.lastCpuUsage = currentCpuUsage
	p.lastRefresh = now
	p.lastCpuUsageFraction = cpuUsage
	fmt.Printf("TIME: %s CPU USAGE: %v MEMORY USAGE: %v\n", time.Now().Format("2006-01-02 15:04:05.000"), p.lastCpuUsageFraction, p.lastMemUsageFraction)
	return nil
}

func (p *Cgroup2SystemInfoSupplier) GetMemoryUsage() (float64, error) {
	if err := p.maybeRefresh(); err != nil {
		return 0, err
	}
	return p.lastMemUsageFraction / 100, nil
}

func (p *Cgroup2SystemInfoSupplier) GetCpuUsage() (float64, error) {
	if err := p.maybeRefresh(); err != nil {
		return 0, err
	}
	return p.lastCpuUsageFraction / 100, nil
}

// readCpuMax reads the cpu.max file to get the CPU quota and period
func readCpuMax(path string) (quota int64, period int64, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid format in cpu.max")
	}

	// Parse the quota (first value)
	if parts[0] == "max" {
		quota = 0 // Unlimited quota
	} else {
		quota, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}

	// Parse the period (second value)
	period, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	return quota, period, nil
}
