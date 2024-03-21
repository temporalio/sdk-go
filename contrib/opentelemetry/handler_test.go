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

package opentelemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"

	"go.temporal.io/sdk/contrib/opentelemetry"
)

func TestTags(t *testing.T) {
	ctx := context.Background()
	metricReader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(metricReader))
	handler := opentelemetry.NewMetricsHandler(
		opentelemetry.MetricsHandlerOptions{
			Meter: meterProvider.Meter("test"),
		},
	)
	handlerWithTag := handler.WithTags(map[string]string{"tag1": "value1"})
	// Emit some values with multiple tags
	handlerWithTag.WithTags(map[string]string{"tag2": "value2"}).Counter("testCounter").Inc(1)
	handlerWithTag.Counter("testCounter").Inc(1)
	// Assert result
	var rm metricdata.ResourceMetrics
	metricReader.Collect(ctx, &rm)
	assert.Len(t, rm.ScopeMetrics, 1)
	metrics := rm.ScopeMetrics[0].Metrics
	assert.Len(t, metrics, 1)
	want := metricdata.Metrics{
		Name: "testCounter",
		Data: metricdata.Sum[int64]{
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: false,
			DataPoints: []metricdata.DataPoint[int64]{
				{
					Attributes: attribute.NewSet(attribute.String("tag1", "value1")),
				},
				{
					Attributes: attribute.NewSet(attribute.String("tag1", "value1"), attribute.String("tag2", "value2")),
				},
			},
		},
	}
	metricdatatest.AssertEqual(t, want, metrics[0], metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreValue())
}

func TestCounterHandler(t *testing.T) {
	ctx := context.Background()
	metricReader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(metricReader))
	handler := opentelemetry.NewMetricsHandler(
		opentelemetry.MetricsHandlerOptions{
			Meter: meterProvider.Meter("test"),
		},
	)
	handlerWithTag := handler.WithTags(map[string]string{"tag1": "value1"})
	// Emit some values
	testCounter := handlerWithTag.Counter("testCounter")
	testCounter.Inc(1)
	testCounter.Inc(1)
	testCounter.Inc(-1)
	// Assert result
	var rm metricdata.ResourceMetrics
	metricReader.Collect(ctx, &rm)
	assert.Len(t, rm.ScopeMetrics, 1)
	metrics := rm.ScopeMetrics[0].Metrics
	assert.Len(t, metrics, 1)
	want := metricdata.Metrics{
		Name: "testCounter",
		Data: metricdata.Sum[int64]{
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: false,
			DataPoints: []metricdata.DataPoint[int64]{
				{
					Value:      1,
					Attributes: attribute.NewSet(attribute.String("tag1", "value1")),
				},
			},
		},
	}
	metricdatatest.AssertEqual(t, want, metrics[0], metricdatatest.IgnoreTimestamp())
}

func TestGaugeHandler(t *testing.T) {
	ctx := context.Background()
	metricReader := metric.NewManualReader()
	meterProvider := metric.NewMeterProvider(metric.WithReader(metricReader))
	handler := opentelemetry.NewMetricsHandler(
		opentelemetry.MetricsHandlerOptions{
			Meter: meterProvider.Meter("test"),
		},
	)
	handlerWithTag := handler.WithTags(map[string]string{"tag1": "value1"})
	// Emit some values
	testGauge := handlerWithTag.Gauge("testGauge")
	testGauge.Update(1)
	testGauge.Update(5)
	testGauge.Update(100)
	// Assert result
	var rm metricdata.ResourceMetrics
	metricReader.Collect(ctx, &rm)
	assert.Len(t, rm.ScopeMetrics, 1)
	metrics := rm.ScopeMetrics[0].Metrics
	assert.Len(t, metrics, 1)
	want := metricdata.Metrics{
		Name: "testGauge",
		Data: metricdata.Gauge[float64]{
			DataPoints: []metricdata.DataPoint[float64]{
				{
					Value:      100,
					Attributes: attribute.NewSet(attribute.String("tag1", "value1")),
				},
			},
		},
	}
	metricdatatest.AssertEqual(t, want, metrics[0], metricdatatest.IgnoreTimestamp())
}

func TestTimerHandler(t *testing.T) {
	testCases := map[string]struct {
		opts opentelemetry.MetricsHandlerOptions
		fn   func(opentelemetry.MetricsHandler)
		want metricdata.Metrics
	}{
		"default buckets": {
			opts: opentelemetry.MetricsHandlerOptions{},
			fn: func(handler opentelemetry.MetricsHandler) {
				handlerWithTag := handler.WithTags(map[string]string{"tag1": "value1"})
				testTimer := handlerWithTag.Timer("testTimer")
				testTimer.Record(time.Millisecond)
				testTimer.Record(time.Second)
				testTimer.Record(time.Hour)
			},
			want: metricdata.Metrics{
				Name: "testTimer",
				Unit: "s",
				Data: metricdata.Histogram[float64]{
					Temporality: metricdata.CumulativeTemporality,
					DataPoints: []metricdata.HistogramDataPoint[float64]{
						{
							Count:        3,
							Sum:          3601.001,
							Min:          metricdata.NewExtrema(time.Millisecond.Seconds()),
							Max:          metricdata.NewExtrema(time.Hour.Seconds()),
							Bounds:       []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000},
							BucketCounts: []uint64{0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0},
							Attributes:   attribute.NewSet(attribute.String("tag1", "value1")),
						},
					},
				},
			},
		},
		"custom buckets": {
			opts: opentelemetry.MetricsHandlerOptions{
				HistogramBucketBoundaries: func(name string) []float64 {
					return []float64{0.5, 1, 2, 3}
				},
			},
			fn: func(handler opentelemetry.MetricsHandler) {
				handlerWithTag := handler.WithTags(map[string]string{"tag1": "value1"})
				testTimer := handlerWithTag.Timer("testTimer")
				testTimer.Record(time.Millisecond)
				testTimer.Record(time.Second)
				testTimer.Record(time.Hour)
			},
			want: metricdata.Metrics{
				Name: "testTimer",
				Unit: "s",
				Data: metricdata.Histogram[float64]{
					Temporality: metricdata.CumulativeTemporality,
					DataPoints: []metricdata.HistogramDataPoint[float64]{
						{
							Count:        3,
							Sum:          3601.001,
							Min:          metricdata.NewExtrema(time.Millisecond.Seconds()),
							Max:          metricdata.NewExtrema(time.Hour.Seconds()),
							Bounds:       []float64{0.5, 1, 2, 3},
							BucketCounts: []uint64{1, 1, 0, 0, 1},
							Attributes:   attribute.NewSet(attribute.String("tag1", "value1")),
						},
					},
				},
			},
		},
	}
	for desc, tc := range testCases {
		t.Run(desc, func(t *testing.T) {
			ctx := context.Background()

			metricReader := metric.NewManualReader()
			meterProvider := metric.NewMeterProvider(metric.WithReader(metricReader))
			tc.opts.Meter = meterProvider.Meter("test")
			handler := opentelemetry.NewMetricsHandler(tc.opts)

			tc.fn(handler)

			var rm metricdata.ResourceMetrics
			metricReader.Collect(ctx, &rm)
			assert.Len(t, rm.ScopeMetrics, 1)
			metrics := rm.ScopeMetrics[0].Metrics
			assert.Len(t, metrics, 1)
			metricdatatest.AssertEqual(t, tc.want, metrics[0], metricdatatest.IgnoreTimestamp())
		})
	}
}
