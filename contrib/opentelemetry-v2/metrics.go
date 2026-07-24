package opentelemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/common/metrics"
)

var _ client.MetricsHandler = MetricsHandler{}

// MetricsHandler implements client.MetricsHandler with OpenTelemetry.
type MetricsHandler struct {
	meter                metric.Meter
	attributes           attribute.Set
	onError              func(error)
	useMonotonicCounters bool
}

// MetricsHandlerOptions configure NewMetricsHandler.
type MetricsHandlerOptions struct {
	// Meter defaults to the global provider's "temporal-sdk-go" meter.
	Meter metric.Meter
	// InitialAttributes are added to every metric.
	InitialAttributes attribute.Set
	// OnError handles meter errors. It defaults to panic.
	OnError func(error)
	// UseMonotonicCounters uses Int64Counter instead of Int64UpDownCounter.
	// Counter increments must then be non-negative. It defaults to false.
	UseMonotonicCounters bool
}

// NewMetricsHandler returns a metrics handler backed by the configured meter.
func NewMetricsHandler(options MetricsHandlerOptions) MetricsHandler {
	if options.Meter == nil {
		options.Meter = otel.GetMeterProvider().Meter("temporal-sdk-go")
	}
	if options.OnError == nil {
		options.OnError = func(err error) { panic(err) }
	}
	return MetricsHandler{
		meter:                options.Meter,
		attributes:           options.InitialAttributes,
		onError:              options.OnError,
		useMonotonicCounters: options.UseMonotonicCounters,
	}
}

// ExtractMetricsHandler returns the wrapped OpenTelemetry handler, if present.
//
// Direct use does not suppress metrics during replay.
func ExtractMetricsHandler(handler client.MetricsHandler) *MetricsHandler {
	for {
		otelHandler, ok := handler.(MetricsHandler)
		if ok {
			return &otelHandler
		}
		unwrappable, _ := handler.(interface{ Unwrap() client.MetricsHandler })
		if unwrappable == nil {
			return nil
		}
		handler = unwrappable.Unwrap()
	}
}

// GetMeter returns the meter used by this handler.
func (m MetricsHandler) GetMeter() metric.Meter {
	return m.meter
}

// GetAttributes returns the attributes set on this handler.
func (m MetricsHandler) GetAttributes() attribute.Set {
	return m.attributes
}

func (m MetricsHandler) WithTags(tags map[string]string) client.MetricsHandler {
	attributes := m.attributes.ToSlice()
	for k, v := range tags {
		attributes = append(attributes, attribute.String(k, v))
	}
	return MetricsHandler{
		meter:                m.meter,
		attributes:           attribute.NewSet(attributes...),
		onError:              m.onError,
		useMonotonicCounters: m.useMonotonicCounters,
	}
}

func (m MetricsHandler) Counter(name string) client.MetricsCounter {
	var c interface {
		Add(context.Context, int64, ...metric.AddOption)
	}
	var err error
	if m.useMonotonicCounters {
		c, err = m.meter.Int64Counter(name)
	} else {
		c, err = m.meter.Int64UpDownCounter(name)
	}
	if err != nil {
		m.onError(err)
		return client.MetricsNopHandler.Counter(name)
	}
	return metrics.CounterFunc(func(d int64) {
		c.Add(context.Background(), d, metric.WithAttributeSet(m.attributes))
	})
}

func (m MetricsHandler) Gauge(name string) client.MetricsGauge {
	g, err := m.meter.Float64Gauge(name)
	if err != nil {
		m.onError(err)
		return client.MetricsNopHandler.Gauge(name)
	}
	return metrics.GaugeFunc(func(f float64) {
		g.Record(context.Background(), f, metric.WithAttributeSet(m.attributes))
	})
}

func (m MetricsHandler) Timer(name string) client.MetricsTimer {
	h, err := m.meter.Float64Histogram(name, metric.WithUnit("s"))
	if err != nil {
		m.onError(err)
		return client.MetricsNopHandler.Timer(name)
	}
	return metrics.TimerFunc(func(t time.Duration) {
		h.Record(context.Background(), t.Seconds(), metric.WithAttributeSet(m.attributes))
	})
}
