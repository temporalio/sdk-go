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

package tally_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally/v4"
	contribtally "go.temporal.io/sdk/contrib/tally"
)

func TestTally(t *testing.T) {
	scope := tally.NewTestScope("", nil)
	handler := contribtally.NewMetricsHandler(scope)
	// Confirm scope is the same
	require.Equal(t, scope, contribtally.ScopeFromHandler(handler))

	handler.Counter("counter_foo").Inc(1)
	handler.Gauge("gauge_foo").Update(2.0)
	handler.Timer("timer_foo").Record(3 * time.Second)
	subHandler := handler.WithTags(map[string]string{"tagkey1": "tagval1"})
	subHandler.Counter("counter_foo").Inc(4)
	subHandler.Gauge("gauge_foo").Update(5.0)
	subHandler.Timer("timer_foo").Record(6 * time.Second)
	subSubHandler := handler.WithTags(map[string]string{"tagkey1": "tagval2", "tagkey2": "tagval2"})
	subSubHandler.Counter("counter_foo").Inc(7)
	subSubHandler.Gauge("gauge_foo").Update(8.0)
	subSubHandler.Timer("timer_foo").Record(9 * time.Second)

	snap := scope.Snapshot()
	// Since Go 1.12, maps are printed in deterministic order
	var metrics []string
	for _, c := range snap.Counters() {
		metrics = append(metrics, fmt.Sprintf("%v: %v - %v", c.Name(), c.Tags(), c.Value()))
	}
	for _, g := range snap.Gauges() {
		metrics = append(metrics, fmt.Sprintf("%v: %v - %v", g.Name(), g.Tags(), g.Value()))
	}
	for _, t := range snap.Timers() {
		metrics = append(metrics, fmt.Sprintf("%v: %v - %v", t.Name(), t.Tags(), t.Values()[0]))
	}
	sort.Strings(metrics)
	require.Equal(t, []string{
		"counter_foo: map[] - 1",
		"counter_foo: map[tagkey1:tagval1] - 4",
		"counter_foo: map[tagkey1:tagval2 tagkey2:tagval2] - 7",
		"gauge_foo: map[] - 2",
		"gauge_foo: map[tagkey1:tagval1] - 5",
		"gauge_foo: map[tagkey1:tagval2 tagkey2:tagval2] - 8",
		"timer_foo: map[] - 3s",
		"timer_foo: map[tagkey1:tagval1] - 6s",
		"timer_foo: map[tagkey1:tagval2 tagkey2:tagval2] - 9s",
	}, metrics)
}
