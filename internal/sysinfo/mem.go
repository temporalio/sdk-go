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

	// This is the kernel's notion of free memory; RAM chips whose bits nobody
	// cares about the value of right now. For a human consumable number,
	// Available is what you really want.
	Free uint64 `json:"free"`

	// OS X / BSD specific numbers:
	// http://www.macyourself.com/2010/02/17/what-is-free-wired-active-and-inactive-system-memory-ram/
	Active   uint64 `json:"active"`
	Inactive uint64 `json:"inactive"`
	Wired    uint64 `json:"wired"`

	// Linux specific numbers
	// https://blogs.oracle.com/linux/understanding-linux-kernel-memory-statistics
	// https://www.kernel.org/doc/Documentation/filesystems/proc.txt
	// https://www.kernel.org/doc/Documentation/vm/overcommit-accounting
	// https://www.kernel.org/doc/Documentation/vm/transhuge.txt
	//
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
