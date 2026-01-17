// SPDX-License-Identifier: BSD-3-Clause
// Derived from github.com/shirou/gopsutil/v4 (Copyright (c) 2014, WAKAYAMA Shirou)
// Modified to include only CPU percentage and memory usage for Linux/Darwin/Windows.
package sysinfo

// VirtualMemoryStat contains memory usage statistics.
type VirtualMemoryStat struct {
	// Total amount of RAM on this system
	Total uint64 `json:"total"`

	// RAM available for programs to allocate
	Available uint64 `json:"available"`

	// RAM used by programs
	Used uint64 `json:"used"`

	// Percentage of RAM used by programs
	UsedPercent float64 `json:"usedPercent"`

	// Kernel's notion of free memory
	Free uint64 `json:"free"`

	// OS X / BSD specific numbers
	Active   uint64 `json:"active"`
	Inactive uint64 `json:"inactive"`
	Wired    uint64 `json:"wired"`

	// Linux specific numbers
	Buffers      uint64 `json:"buffers"`
	Cached       uint64 `json:"cached"`
	WriteBack    uint64 `json:"writeBack"`
	Dirty        uint64 `json:"dirty"`
	WriteBackTmp uint64 `json:"writeBackTmp"`
	Shared       uint64 `json:"shared"`
	Slab         uint64 `json:"slab"`
	Sreclaimable uint64 `json:"sreclaimable"`
	Sunreclaim   uint64 `json:"sunreclaim"`
	PageTables   uint64 `json:"pageTables"`
	SwapCached   uint64 `json:"swapCached"`
	CommitLimit  uint64 `json:"commitLimit"`
	CommittedAS  uint64 `json:"committedAS"`
}
