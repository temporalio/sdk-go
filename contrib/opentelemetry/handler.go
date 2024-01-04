// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package opentelemetry

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/common/metrics"
)

type MetricsHandler struct {
	// Meter to create metrics
	Meter metric.Meter
	// Attributes currently set on the handler
	Attributes attribute.Set
}

// NewMetricsHandler returns a client.MetricsHandler that is backed by the given Meter
func NewMetricsHandler(meter metric.Meter) client.MetricsHandler {
	if meter == nil {
		meter = otel.GetMeterProvider().Meter("temporal-sdk-go")
	}
	return MetricsHandler{
		Meter: meter,
	}
}

// ExtractHandler gets the underlying Open Telemetry MetricsHandler from a MetricsHandler
// if any is present.
//
// Raw use of the MetricHandler is discouraged but may be used for Histograms or other
// advanced features. This scope does not skip metrics during replay like the
// metrics handler does. Therefore the caller should check replay state.
func ExtractHandler(handler client.MetricsHandler) *MetricsHandler {
	// Continually unwrap until we find an instance of our own handler
	for {
		otelHandler, ok := handler.(MetricsHandler)
		if ok {
			return &otelHandler
		}
		// If unwrappable, do so, otherwise return noop
		unwrappable, _ := handler.(interface{ Unwrap() client.MetricsHandler })
		if unwrappable == nil {
			return nil
		}
		handler = unwrappable.Unwrap()
	}
}

func (m MetricsHandler) WithTags(tags map[string]string) client.MetricsHandler {
	attributes := m.Attributes.ToSlice()
	for k, v := range tags {
		attributes = append(attributes, attribute.String(k, v))
	}
	return MetricsHandler{
		Meter:      m.Meter,
		Attributes: attribute.NewSet(attributes...),
	}
}

func (m MetricsHandler) Counter(name string) client.MetricsCounter {
	// TODO handle error?
	c, _ := m.Meter.Int64UpDownCounter(name)
	return metrics.CounterFunc(func(d int64) {
		c.Add(context.Background(), d, metric.WithAttributeSet(m.Attributes))
	})
}

func (m MetricsHandler) Gauge(name string) client.MetricsGauge {
	// TODO(https://github.com/open-telemetry/opentelemetry-go/issues/3984) replace with sync gauge once supported
	var config atomic.Value
	config.Store(0.0)
	m.Meter.Float64ObservableGauge(name,
		metric.WithFloat64Callback(func(ctx context.Context, o metric.Float64Observer) error {
			o.Observe(config.Load().(float64), metric.WithAttributeSet(m.Attributes))
			return nil
		}))
	return metrics.GaugeFunc(func(f float64) {
		config.Store(f)
	})
}

func (m MetricsHandler) Timer(name string) client.MetricsTimer {
	// TODO handle error?
	h, _ := m.Meter.Float64Histogram(name, metric.WithUnit("s"))
	return metrics.TimerFunc(func(t time.Duration) {
		h.Record(context.Background(), t.Seconds(), metric.WithAttributeSet(m.Attributes))
	})
}
