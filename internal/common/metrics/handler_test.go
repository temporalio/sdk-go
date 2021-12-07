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

package metrics_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
)

func TestReplayAwareHandler(t *testing.T) {
	var replaying bool
	capture := metrics.NewCapturingHandler()
	handler := metrics.NewReplayAwareHandler(&replaying, capture)

	// As replaying
	replaying = true
	handler.Counter("counter1").Inc(1)
	handler.Gauge("gauge1").Update(2.0)
	handler.Timer("timer1").Record(3 * time.Second)
	require.Len(t, capture.Counters(), 1)
	require.Equal(t, int64(0), capture.Counters()[0].Value())
	require.Len(t, capture.Gauges(), 1)
	require.Equal(t, 0.0, capture.Gauges()[0].Value())
	require.Len(t, capture.Timers(), 1)
	require.Equal(t, 0*time.Second, capture.Timers()[0].Value())

	// As not replaying
	replaying = false
	handler.Counter("counter1").Inc(4)
	handler.Gauge("gauge1").Update(5.0)
	handler.Timer("timer1").Record(6 * time.Second)
	require.Len(t, capture.Counters(), 1)
	require.Equal(t, int64(4), capture.Counters()[0].Value())
	require.Len(t, capture.Gauges(), 1)
	require.Equal(t, 5.0, capture.Gauges()[0].Value())
	require.Len(t, capture.Timers(), 1)
	require.Equal(t, 6*time.Second, capture.Timers()[0].Value())
}
