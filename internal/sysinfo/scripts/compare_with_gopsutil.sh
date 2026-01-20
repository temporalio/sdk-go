#!/bin/bash
# Compare internal/sysinfo implementation against gopsutil
# Usage: ./internal/sysinfo/scripts/compare_with_gopsutil.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYSINFO_DIR="$(dirname "$SCRIPT_DIR")"
REPO_ROOT="$(cd "$SYSINFO_DIR/../.." && pwd)"

TEST_FILE="$SYSINFO_DIR/compare_test.go"

cleanup() {
    echo "Cleaning up..."
    rm -f "$TEST_FILE"
    cd "$REPO_ROOT" && go mod tidy 2>/dev/null
    echo "Done."
}

trap cleanup EXIT

echo "=== Comparing internal/sysinfo against gopsutil ==="
echo ""

# Create the comparison test file
cat > "$TEST_FILE" << 'TESTEOF'
//go:build compare_gopsutil

package sysinfo_test

import (
	"context"
	"math"
	"testing"
	"time"

	gopsutil_cpu "github.com/shirou/gopsutil/v4/cpu"
	gopsutil_mem "github.com/shirou/gopsutil/v4/mem"
	"go.temporal.io/sdk/internal/sysinfo"
)

func TestCPUTimesMatchGopsutil(t *testing.T) {
	ctx := context.Background()

	t.Run("total", func(t *testing.T) {
		gTimes, gErr := gopsutil_cpu.TimesWithContext(ctx, false)
		sTimes, sErr := sysinfo.TimesWithContext(ctx, false)

		if gErr != nil || sErr != nil {
			t.Fatalf("errors: gopsutil=%v, sysinfo=%v", gErr, sErr)
		}

		if len(gTimes) != len(sTimes) {
			t.Fatalf("length mismatch: gopsutil=%d, sysinfo=%d", len(gTimes), len(sTimes))
		}

		g, s := gTimes[0], sTimes[0]
		t.Logf("gopsutil: CPU=%s User=%.4f System=%.4f Idle=%.4f Nice=%.4f Iowait=%.4f",
			g.CPU, g.User, g.System, g.Idle, g.Nice, g.Iowait)
		t.Logf("sysinfo:  CPU=%s User=%.4f System=%.4f Idle=%.4f Nice=%.4f Iowait=%.4f",
			s.CPU, s.User, s.System, s.Idle, s.Nice, s.Iowait)

		assertClose(t, "User", g.User, s.User, 0.01)
		assertClose(t, "System", g.System, s.System, 0.01)
		assertClose(t, "Idle", g.Idle, s.Idle, 0.01)
		assertClose(t, "Nice", g.Nice, s.Nice, 0.01)
		assertClose(t, "Iowait", g.Iowait, s.Iowait, 0.01)
	})

	t.Run("percpu", func(t *testing.T) {
		gTimes, gErr := gopsutil_cpu.TimesWithContext(ctx, true)
		sTimes, sErr := sysinfo.TimesWithContext(ctx, true)

		if gErr != nil || sErr != nil {
			t.Fatalf("errors: gopsutil=%v, sysinfo=%v", gErr, sErr)
		}

		if len(gTimes) != len(sTimes) {
			t.Fatalf("length mismatch: gopsutil=%d, sysinfo=%d", len(gTimes), len(sTimes))
		}

		t.Logf("Found %d CPUs", len(gTimes))
		for i := range gTimes {
			g, s := gTimes[i], sTimes[i]
			if g.CPU != s.CPU {
				t.Errorf("CPU[%d] name mismatch: gopsutil=%s, sysinfo=%s", i, g.CPU, s.CPU)
			}
			assertClose(t, "User", g.User, s.User, 0.01)
			assertClose(t, "System", g.System, s.System, 0.01)
			assertClose(t, "Idle", g.Idle, s.Idle, 0.01)
		}
	})
}

func TestCPUPercentMatchesGopsutil(t *testing.T) {
	ctx := context.Background()

	t.Run("with_interval", func(t *testing.T) {
		interval := 200 * time.Millisecond

		// Run both concurrently so they measure the same time window
		var gPercent, sPercent []float64
		var gErr, sErr error

		done := make(chan struct{})
		go func() {
			gPercent, gErr = gopsutil_cpu.PercentWithContext(ctx, interval, false)
			done <- struct{}{}
		}()
		go func() {
			sPercent, sErr = sysinfo.PercentWithContext(ctx, interval, false)
			done <- struct{}{}
		}()
		<-done
		<-done

		if gErr != nil || sErr != nil {
			t.Fatalf("errors: gopsutil=%v, sysinfo=%v", gErr, sErr)
		}

		if len(gPercent) != len(sPercent) {
			t.Fatalf("length mismatch: gopsutil=%d, sysinfo=%d", len(gPercent), len(sPercent))
		}

		t.Logf("gopsutil CPU%%: %.2f", gPercent[0])
		t.Logf("sysinfo CPU%%:  %.2f", sPercent[0])

		// Allow some variance since measurements aren't perfectly synchronized
		if math.Abs(gPercent[0]-sPercent[0]) > 15.0 {
			t.Errorf("CPU percent differs by more than 15%%: gopsutil=%.2f, sysinfo=%.2f",
				gPercent[0], sPercent[0])
		}
	})

	t.Run("without_interval", func(t *testing.T) {
		gopsutil_cpu.PercentWithContext(ctx, 0, false)
		sysinfo.PercentWithContext(ctx, 0, false)

		time.Sleep(50 * time.Millisecond)

		gPercent, gErr := gopsutil_cpu.PercentWithContext(ctx, 0, false)
		sPercent, sErr := sysinfo.PercentWithContext(ctx, 0, false)

		if gErr != nil || sErr != nil {
			t.Fatalf("errors: gopsutil=%v, sysinfo=%v", gErr, sErr)
		}

		t.Logf("gopsutil CPU%% (cached): %.2f", gPercent[0])
		t.Logf("sysinfo CPU%% (cached):  %.2f", sPercent[0])

		if gPercent[0] < 0 || gPercent[0] > 100 {
			t.Errorf("gopsutil returned invalid percent: %.2f", gPercent[0])
		}
		if sPercent[0] < 0 || sPercent[0] > 100 {
			t.Errorf("sysinfo returned invalid percent: %.2f", sPercent[0])
		}
	})
}

func TestMemoryMatchesGopsutil(t *testing.T) {
	ctx := context.Background()

	gMem, gErr := gopsutil_mem.VirtualMemoryWithContext(ctx)
	sMem, sErr := sysinfo.VirtualMemoryWithContext(ctx)

	if gErr != nil || sErr != nil {
		t.Fatalf("errors: gopsutil=%v, sysinfo=%v", gErr, sErr)
	}

	t.Logf("gopsutil: Total=%d Available=%d Used=%d UsedPercent=%.2f Free=%d",
		gMem.Total, gMem.Available, gMem.Used, gMem.UsedPercent, gMem.Free)
	t.Logf("sysinfo:  Total=%d Available=%d Used=%d UsedPercent=%.2f Free=%d",
		sMem.Total, sMem.Available, sMem.Used, sMem.UsedPercent, sMem.Free)

	// Total should be exactly the same (doesn't change)
	if gMem.Total != sMem.Total {
		t.Errorf("Total mismatch: gopsutil=%d, sysinfo=%d", gMem.Total, sMem.Total)
	}

	// Other memory values can change between calls, allow 0.1% tolerance
	tolerance := float64(gMem.Total) * 0.001

	if math.Abs(float64(gMem.Available)-float64(sMem.Available)) > tolerance {
		t.Errorf("Available differs by more than 0.1%%: gopsutil=%d, sysinfo=%d", gMem.Available, sMem.Available)
	}

	if math.Abs(float64(gMem.Used)-float64(sMem.Used)) > tolerance {
		t.Errorf("Used differs by more than 0.1%%: gopsutil=%d, sysinfo=%d", gMem.Used, sMem.Used)
	}

	if math.Abs(gMem.UsedPercent-sMem.UsedPercent) > 0.1 {
		t.Errorf("UsedPercent mismatch: gopsutil=%.4f, sysinfo=%.4f", gMem.UsedPercent, sMem.UsedPercent)
	}

	if math.Abs(float64(gMem.Free)-float64(sMem.Free)) > tolerance {
		t.Errorf("Free differs by more than 0.1%%: gopsutil=%d, sysinfo=%d", gMem.Free, sMem.Free)
	}
}

func assertClose(t *testing.T, name string, expected, actual, tolerance float64) {
	t.Helper()
	if expected == 0 && actual == 0 {
		return
	}
	diff := math.Abs(expected - actual)
	relativeDiff := diff / math.Max(math.Abs(expected), 1.0)
	if relativeDiff > tolerance {
		t.Errorf("%s: values differ by %.2f%% (expected=%.4f, actual=%.4f)",
			name, relativeDiff*100, expected, actual)
	}
}
TESTEOF

echo "1. Created comparison test file"

# Add gopsutil dependency
cd "$REPO_ROOT"
echo "2. Adding gopsutil dependency..."
go get github.com/shirou/gopsutil/v4@v4.24.8 2>/dev/null

echo "3. Running go mod tidy..."
go mod tidy 2>/dev/null

echo "4. Running comparison tests..."
echo ""
go test -v -tags=compare_gopsutil ./internal/sysinfo/...
TEST_RESULT=$?

echo ""
if [ $TEST_RESULT -eq 0 ]; then
    echo "=== All comparisons PASSED ==="
else
    echo "=== Some comparisons FAILED ==="
fi

exit $TEST_RESULT
