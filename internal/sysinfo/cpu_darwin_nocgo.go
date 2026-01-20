// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
//go:build darwin && !cgo

package sysinfo

func perCPUTimes() ([]TimesStat, error) {
	return nil, ErrNotImplemented
}

func allCPUTimes() ([]TimesStat, error) {
	return nil, ErrNotImplemented
}
