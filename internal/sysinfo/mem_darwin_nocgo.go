// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin && !cgo

package sysinfo

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func VirtualMemory() (*VirtualMemoryStat, error) {
	return VirtualMemoryWithContext(context.Background())
}

func VirtualMemoryWithContext(ctx context.Context) (*VirtualMemoryStat, error) {
	ret := &VirtualMemoryStat{}

	total, err := getHwMemsize()
	if err != nil {
		return nil, err
	}
	ret.Total = total

	err = getVMStat(ctx, ret)
	if err != nil {
		return nil, err
	}

	ret.Available = ret.Free + ret.Inactive
	ret.Used = ret.Total - ret.Available
	if ret.Total > 0 {
		ret.UsedPercent = 100 * float64(ret.Used) / float64(ret.Total)
	}

	return ret, nil
}

func getVMStat(ctx context.Context, vms *VirtualMemoryStat) error {
	cmd := exec.CommandContext(ctx, "vm_stat")
	out, err := cmd.Output()
	if err != nil {
		return err
	}

	pagesize := uint64(unix.Getpagesize())
	lines := strings.Split(string(out), "\n")

	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSpace(fields[0])
		value := strings.Trim(fields[1], " .")

		switch key {
		case "Pages free":
			if v, err := strconv.ParseUint(value, 10, 64); err == nil {
				vms.Free = v * pagesize
			}
		case "Pages inactive":
			if v, err := strconv.ParseUint(value, 10, 64); err == nil {
				vms.Inactive = v * pagesize
			}
		case "Pages active":
			if v, err := strconv.ParseUint(value, 10, 64); err == nil {
				vms.Active = v * pagesize
			}
		case "Pages wired down":
			if v, err := strconv.ParseUint(value, 10, 64); err == nil {
				vms.Wired = v * pagesize
			}
		}
	}

	return nil
}
