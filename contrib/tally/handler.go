package tally

import (
	"github.com/uber-go/tally/v4"
	"go.temporal.io/sdk/client"
)

type metricsHandler struct{ scope tally.Scope }

func NewMetricsHandler(scope tally.Scope) client.MetricsHandler {
	return metricsHandler{scope}
}

func (m metricsHandler) WithTags(tags map[string]string) client.MetricsHandler {
	return metricsHandler{m.scope.Tagged(tags)}
}

func (m metricsHandler) Counter(name string) client.MetricsCounter {
	return m.scope.Counter(name)
}

func (m metricsHandler) Gauge(name string) client.MetricsGauge {
	return m.scope.Gauge(name)
}

func (m metricsHandler) Timer(name string) client.MetricsTimer {
	return m.scope.Timer(name)
}
