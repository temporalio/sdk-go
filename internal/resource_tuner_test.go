package internal

import (
	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/log"
	"testing"
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
	metricsHandler := metrics.NopHandler
	fakeSupplier := &FakeSystemInfoSupplier{memUse: 0.5, cpuUse: 0.5}
	rcOpts := DefaultResourceControllerOptions()
	rcOpts.MemTargetPercent = 0.8
	rcOpts.CpuTargetPercent = 0.9
	rcOpts.InfoSupplier = fakeSupplier
	rc := NewResourceController(rcOpts)

	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger, metricsHandler)
		assert.NoError(t, err)
		assert.True(t, decision)

		assert.InDelta(t, 1.5, rc.memPid.controlSignal, 0.001)
		assert.InDelta(t, 2.0, rc.cpuPid.controlSignal, 0.001)
	}

	fakeSupplier.memUse = 0.8
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger, metricsHandler)
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger, metricsHandler)
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.7
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision(logger, metricsHandler)
		assert.NoError(t, err)
		assert.True(t, decision)
	}
}

func TestPidDecisionEmitsUsageMetrics(t *testing.T) {
	logger := &log.NoopLogger{}
	metricsHandler := metrics.NewCapturingHandler()
	fakeSupplier := &FakeSystemInfoSupplier{memUse: 0.25, cpuUse: 0.75}

	rcOpts := DefaultResourceControllerOptions()
	rcOpts.InfoSupplier = fakeSupplier
	rc := NewResourceController(rcOpts)

	_, err := rc.pidDecision(logger, metricsHandler)
	assert.NoError(t, err)

	gauges := metricsHandler.Gauges()
	assert.Len(t, gauges, 2)

	gaugesByName := make(map[string]float64)
	for _, gauge := range gauges {
		gaugesByName[gauge.Name] = gauge.Value()
	}

	assert.Equal(t, 25.0, gaugesByName[resourceSlotsMemUsage])
	assert.Equal(t, 75.0, gaugesByName[resourceSlotsCPUUsage])

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.9
	_, err = rc.pidDecision(logger, metricsHandler)
	assert.NoError(t, err)

	gauges = metricsHandler.Gauges()
	assert.Len(t, gauges, 2)

	gaugesByName = make(map[string]float64)
	for _, gauge := range gauges {
		gaugesByName[gauge.Name] = gauge.Value()
	}

	assert.Equal(t, 70.0, gaugesByName[resourceSlotsMemUsage])
	assert.Equal(t, 90.0, gaugesByName[resourceSlotsCPUUsage])
}
