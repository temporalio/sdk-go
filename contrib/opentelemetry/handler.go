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

var _ client.MetricsHandler = MetricsHandler{}

// MetricsHandler is an implementation of client.MetricsHandler
// for open telemetry.
type MetricsHandler struct {
	meter      metric.Meter
	attributes attribute.Set
	onError    func(error)
}

// MetricsHandlerOptions are options provided to NewMetricsHandler.
type MetricsHandlerOptions struct {
	// Meter is the Meter to use. If not set, one is obtained from the global
	// meter provider using the name "temporal-sdk-go".
	Meter metric.Meter
	// InitialAttributes to set on the handler
	// Optional: Defaults to the empty set.
	InitialAttributes attribute.Set
	// OnError Callback to invoke if the provided meter returns an error.
	// Optional: Defaults to panicking on any error.
	OnError func(error)
}

// NewMetricsHandler returns a client.MetricsHandler that is backed by the given Meter
func NewMetricsHandler(options MetricsHandlerOptions) MetricsHandler {
	if options.Meter == nil {
		options.Meter = otel.GetMeterProvider().Meter("temporal-sdk-go")
	}
	if options.OnError == nil {
		options.OnError = func(err error) { panic(err) }
	}
	return MetricsHandler{
		meter:      options.Meter,
		attributes: options.InitialAttributes,
		onError:    options.OnError,
	}
}

// ExtractMetricsHandler gets the underlying Open Telemetry MetricsHandler from a MetricsHandler
// if any is present.
//
// Raw use of the MetricHandler is discouraged but may be used for Histograms or other
// advanced features. This scope does not skip metrics during replay like the
// metrics handler does. Therefore the caller should check replay state.
func ExtractMetricsHandler(handler client.MetricsHandler) *MetricsHandler {
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
		meter:      m.meter,
		attributes: attribute.NewSet(attributes...),
		onError:    m.onError,
	}
}

func (m MetricsHandler) Counter(name string) client.MetricsCounter {
	c, err := m.meter.Int64UpDownCounter(name)
	if err != nil {
		m.onError(err)
	}
	return metrics.CounterFunc(func(d int64) {
		c.Add(context.Background(), d, metric.WithAttributeSet(m.attributes))
	})
}

func (m MetricsHandler) Gauge(name string) client.MetricsGauge {
	// TODO(https://github.com/open-telemetry/opentelemetry-go/issues/3984) replace with sync gauge once supported
	var config atomic.Value
	config.Store(0.0)
	m.meter.Float64ObservableGauge(name,
		metric.WithFloat64Callback(func(ctx context.Context, o metric.Float64Observer) error {
			o.Observe(config.Load().(float64), metric.WithAttributeSet(m.attributes))
			return nil
		}))
	return metrics.GaugeFunc(func(f float64) {
		config.Store(f)
	})
}

func (m MetricsHandler) Timer(name string) client.MetricsTimer {
	h, err := m.meter.Float64Histogram(name, metric.WithUnit("s"))
	if err != nil {
		m.onError(err)
	}
	return metrics.TimerFunc(func(t time.Duration) {
		h.Record(context.Background(), t.Seconds(), metric.WithAttributeSet(m.attributes))
	})
}
