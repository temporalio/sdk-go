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

// Package tally implements a MetricsHandler backed by github.com/uber-go/tally.
package tally

import (
	"github.com/uber-go/tally/v4"
	"go.temporal.io/sdk/client"
)

type metricsHandler struct{ scope tally.Scope }

// NewMetricsHandler returns a MetricsHandler that is backed by the given Tally
// scope.
//
// Default metrics are Prometheus compatible but default separator (.) should be
// replaced with some other character:
// 	opts := tally.ScopeOptions{
// 		Separator: "_",
// 	}
// 	scope, _ := tally.NewRootScope(opts, time.Second)
//
// If you have custom metrics make sure they are compatible with Prometheus or
// create tally scope with sanitizer options set:
// 	var (
// 		safeCharacters = []rune{'_'}
// 		sanitizeOptions = tally.SanitizeOptions{
// 			NameCharacters: tally.ValidCharacters{
// 				Ranges:     tally.AlphanumericRange,
// 				Characters: safeCharacters,
// 			},
// 			KeyCharacters: tally.ValidCharacters{
// 				Ranges:     tally.AlphanumericRange,
// 				Characters: safeCharacters,
// 			},
// 			ValueCharacters: tally.ValidCharacters{
// 				Ranges:     tally.AlphanumericRange,
// 				Characters: safeCharacters,
// 			},
// 			ReplacementCharacter: tally.DefaultReplacementCharacter,
// 		}
// 	)
// 	opts := tally.ScopeOptions{
// 		SanitizeOptions: &sanitizeOptions,
// 		Separator: "_",
// 	}
// 	scope, _ := tally.NewRootScope(opts, time.Second)
func NewMetricsHandler(scope tally.Scope) client.MetricsHandler {
	return metricsHandler{scope}
}

// ScopeFromHandler returns the underlying scope of the handler. Callers may
// need to check workflow.IsReplaying(ctx) to avoid recording metrics during
// replay. If this handler was not created via this package, tally.NoopScope is
// returned.
//
// Raw use of the scope is discouraged but may be used for Histograms or other
// advanced features. This scope does not skip metrics during replay like the
// metrics handler does. Therefore the caller should check replay state, for
// example:
// 	scope := tally.NoopScope
// 	if !workflow.IsReplaying(ctx) {
// 		scope = ScopeFromHandler(workflow.GetMetricsHandler(ctx))
// 	}
// 	scope.Histogram("my_histogram", nil).RecordDuration(5 * time.Second)
func ScopeFromHandler(handler client.MetricsHandler) tally.Scope {
	// Continually unwrap until we find an instance of our own handler
	for {
		tallyHandler, ok := handler.(metricsHandler)
		if ok {
			return tallyHandler.scope
		}
		// If unwrappable, do so, otherwise return noop
		unwrappable, _ := handler.(interface{ Unwrap() client.MetricsHandler })
		if unwrappable == nil {
			return tally.NoopScope
		}
		handler = unwrappable.Unwrap()
	}
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
