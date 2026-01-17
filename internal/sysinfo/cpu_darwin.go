// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin

package sysinfo

import (
	"context"
)

// ClocksPerSec is the number of clock ticks per second.
// On macOS, this is typically 100.
var ClocksPerSec = float64(100)

func Times(percpu bool) ([]TimesStat, error) {
	return TimesWithContext(context.Background(), percpu)
}

func TimesWithContext(ctx context.Context, percpu bool) ([]TimesStat, error) {
	if percpu {
		return perCPUTimes()
	}
	return allCPUTimes()
}
