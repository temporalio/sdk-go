// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build linux

package sysinfo

import (
	"context"
	"strconv"
	"strings"
)

func VirtualMemory() (*VirtualMemoryStat, error) {
	return VirtualMemoryWithContext(context.Background())
}

func VirtualMemoryWithContext(ctx context.Context) (*VirtualMemoryStat, error) {
	filename := HostProcWithContext(ctx, "meminfo")
	lines, err := ReadLines(filename)
	if err != nil {
		return nil, err
	}

	ret := &VirtualMemoryStat{}
	var memAvailable, memFree, buffers, cached, sReclaimable uint64
	memAvailablePresent := false

	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) != 2 {
			continue
		}
		key := strings.TrimSpace(fields[0])
		value := strings.TrimSpace(fields[1])
		value = strings.Replace(value, " kB", "", -1)

		v, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			continue
		}
		v *= 1024 // Convert kB to bytes

		switch key {
		case "MemTotal":
			ret.Total = v
		case "MemFree":
			memFree = v
			ret.Free = v
		case "MemAvailable":
			memAvailablePresent = true
			memAvailable = v
		case "Buffers":
			buffers = v
			ret.Buffers = v
		case "Cached":
			cached = v
			ret.Cached = v
		case "Active":
			ret.Active = v
		case "Inactive":
			ret.Inactive = v
		case "Writeback":
			ret.WriteBack = v
		case "WritebackTmp":
			ret.WriteBackTmp = v
		case "Dirty":
			ret.Dirty = v
		case "Shmem":
			ret.Shared = v
		case "Slab":
			ret.Slab = v
		case "SReclaimable":
			sReclaimable = v
			ret.Sreclaimable = v
		case "SUnreclaim":
			ret.Sunreclaim = v
		case "PageTables":
			ret.PageTables = v
		case "SwapCached":
			ret.SwapCached = v
		case "CommitLimit":
			ret.CommitLimit = v
		case "Committed_AS":
			ret.CommittedAS = v
		}
	}

	// Calculate Available if not present (kernel < 3.14)
	if memAvailablePresent {
		ret.Available = memAvailable
	} else {
		ret.Available = memFree + buffers + cached + sReclaimable
	}

	// Calculate Used and UsedPercent
	ret.Used = ret.Total - ret.Available
	if ret.Total > 0 {
		ret.UsedPercent = float64(ret.Used) / float64(ret.Total) * 100.0
	}

	return ret, nil
}
