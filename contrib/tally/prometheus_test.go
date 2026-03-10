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
