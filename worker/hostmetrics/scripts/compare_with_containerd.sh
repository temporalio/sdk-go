#!/bin/bash
# Compare direct cgroup reads against containerd/cgroups/v3
# Must run inside a Linux container with cgroup v2 and resource limits set
#
# Usage: ./worker/hostmetrics/scripts/compare_with_containerd.sh
#
# Or via Docker:
#   docker run --rm -v "$(pwd)":/workspace -w /workspace \
#       --memory=512m --cpus=1 golang:1.23 \
#       ./worker/hostmetrics/scripts/compare_with_containerd.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOSTMETRICS_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(cd "$HOSTMETRICS_DIR/../.." && pwd)"

TEST_FILE="$HOSTMETRICS_DIR/compare_cgroups_test.go"

cleanup() {
    echo "Cleaning up..."
    rm -f "$TEST_FILE"
    cd "$REPO_ROOT" && go mod tidy 2>/dev/null
    echo "Done."
}

trap cleanup EXIT

echo "=== Comparing cgroup implementation against containerd/cgroups ==="
echo ""

if [[ "$(uname)" != "Linux" ]]; then
    echo "ERROR: This test must run on Linux (inside a container)"
    exit 1
fi

if [[ ! -f /sys/fs/cgroup/memory.current ]]; then
    echo "ERROR: cgroup v2 not available (missing /sys/fs/cgroup/memory.current)"
    exit 1
fi

echo "1. Cgroup v2 files found:"
echo "   memory.current: $(cat /sys/fs/cgroup/memory.current)"
echo "   memory.max: $(cat /sys/fs/cgroup/memory.max)"
echo "   cpu.stat usage_usec: $(grep usage_usec /sys/fs/cgroup/cpu.stat | awk '{print $2}')"
echo "   cpu.max: $(cat /sys/fs/cgroup/cpu.max 2>/dev/null || echo 'not set')"
echo ""

# Create the comparison test file
cat > "$TEST_FILE" << 'TESTEOF'
//go:build linux && compare_cgroups

package hostmetrics

import (
	"testing"

	"github.com/containerd/cgroups/v3/cgroup2"
)

func TestCgroupMemoryMatchesContainerd(t *testing.T) {
	// Get values from containerd/cgroups
	control, err := cgroup2.Load("/")
	if err != nil {
		t.Skipf("Not in cgroup v2 environment: %v", err)
	}
	metrics, err := control.Stat()
	if err != nil {
		t.Fatalf("containerd Stat() failed: %v", err)
	}

	// Get values from our direct reads
	memUsage, memLimit, err := readMemoryStat()
	if err != nil {
		t.Fatalf("readMemoryStat() failed: %v", err)
	}

	t.Logf("containerd: Usage=%d UsageLimit=%d", metrics.Memory.Usage, metrics.Memory.UsageLimit)
	t.Logf("direct:     Usage=%d UsageLimit=%d", memUsage, memLimit)

	// Memory usage can change between reads, allow 1MB tolerance
	if absDiff(metrics.Memory.Usage, memUsage) > 1024*1024 {
		t.Errorf("Memory usage mismatch: containerd=%d, direct=%d (diff=%d)",
			metrics.Memory.Usage, memUsage, absDiff(metrics.Memory.Usage, memUsage))
	}

	// Memory limit should match exactly (or both be 0/max for unlimited)
	// containerd returns max uint64 for unlimited, we return 0
	containerdLimit := metrics.Memory.UsageLimit
	if containerdLimit == ^uint64(0) {
		containerdLimit = 0 // Treat max uint64 as unlimited (0)
	}
	if containerdLimit != memLimit {
		t.Errorf("Memory limit mismatch: containerd=%d, direct=%d",
			metrics.Memory.UsageLimit, memLimit)
	}
}

func TestCgroupCPUMatchesContainerd(t *testing.T) {
	control, err := cgroup2.Load("/")
	if err != nil {
		t.Skipf("Not in cgroup v2 environment: %v", err)
	}
	metrics, err := control.Stat()
	if err != nil {
		t.Fatalf("containerd Stat() failed: %v", err)
	}

	cpuUsage, err := readCPUUsage()
	if err != nil {
		t.Fatalf("readCPUUsage() failed: %v", err)
	}

	t.Logf("containerd: UsageUsec=%d", metrics.CPU.UsageUsec)
	t.Logf("direct:     UsageUsec=%d", cpuUsage)

	// CPU usage increases over time, allow 100ms tolerance for timing between reads
	if absDiff(metrics.CPU.UsageUsec, cpuUsage) > 100000 {
		t.Errorf("CPU usage mismatch: containerd=%d, direct=%d (diff=%d)",
			metrics.CPU.UsageUsec, cpuUsage, absDiff(metrics.CPU.UsageUsec, cpuUsage))
	}
}

func absDiff(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}
TESTEOF

echo "2. Created comparison test file"

cd "$REPO_ROOT"
echo "3. Adding containerd/cgroups dependency..."
go get github.com/containerd/cgroups/v3@v3.0.3 2>/dev/null

echo "4. Running go mod tidy..."
go mod tidy 2>/dev/null

echo "5. Running comparison tests..."
echo ""
go test -v -tags=compare_cgroups ./worker/hostmetrics/...
TEST_RESULT=$?

echo ""
if [ $TEST_RESULT -eq 0 ]; then
    echo "=== All comparisons PASSED ==="
else
    echo "=== Some comparisons FAILED ==="
fi

exit $TEST_RESULT
