package resourcetuner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/internal/log"
)

type FakeSystemInfoSupplier struct {
	memUse float64
	cpuUse float64
}

func (f FakeSystemInfoSupplier) GetMemoryUsage(_ *SystemInfoContext) (float64, error) {
	return f.memUse, nil
}

func (f FakeSystemInfoSupplier) GetCpuUsage(_ *SystemInfoContext) (float64, error) {
	return f.cpuUse, nil
}

func TestPidDecisions(t *testing.T) {
	logger := &log.NoopLogger{}
	fakeSupplier := &FakeSystemInfoSupplier{memUse: 0.5, cpuUse: 0.5}
	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = fakeSupplier
	rc := NewResourceController(rcOpts)

	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger)
		assert.NoError(t, err)
		assert.True(t, decision)

		assert.InDelta(t, 1.5, rc.memPid.State.ControlSignal, 0.001)
		assert.InDelta(t, 2.0, rc.cpuPid.State.ControlSignal, 0.001)
	}

	fakeSupplier.memUse = 0.8
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger)
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger)
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.7
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger)
		assert.NoError(t, err)
		assert.True(t, decision)
	}
}
