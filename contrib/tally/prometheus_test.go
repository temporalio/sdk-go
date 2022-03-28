// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

func TestPrometheusNaming(t *testing.T) {
	scope, _ := tally.NewRootScope(tally.ScopeOptions{
		SanitizeOptions: &contribtally.PrometheusSanitizeOptions,
		Separator:       "_",
	}, time.Second)
	testScope := scope.(tally.TestScope)
	scope = contribtally.NewPrometheusNamingScope(scope)
	handler := contribtally.NewMetricsHandler(scope)

	handler.Counter("counter_foo.bar").Inc(1)
	handler.Gauge("gauge_foo.bar").Update(2.0)
	handler.Timer("timer_foo.bar").Record(3 * time.Second)

	snap := testScope.Snapshot()
	var metrics []string
	for _, c := range snap.Counters() {
		metrics = append(metrics, fmt.Sprintf("%v - %v", c.Name(), c.Value()))
	}
	for _, g := range snap.Gauges() {
		metrics = append(metrics, fmt.Sprintf("%v - %v", g.Name(), g.Value()))
	}
	for _, t := range snap.Timers() {
		metrics = append(metrics, fmt.Sprintf("%v - %v", t.Name(), t.Values()[0]))
	}
	sort.Strings(metrics)
	require.Equal(t, []string{
		"counter_foo_bar_total - 1",
		"gauge_foo_bar - 2",
		"timer_foo_bar_seconds - 3s",
	}, metrics)
}
