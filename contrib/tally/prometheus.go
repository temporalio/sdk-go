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

package tally

import (
	"strings"

	"github.com/uber-go/tally/v4"
)

// PrometheusSanitizeOptions is the set of sanitize options for
// tally.ScopeOptions to match Prometheus naming rules.
var PrometheusSanitizeOptions = tally.SanitizeOptions{
	NameCharacters:       tally.ValidCharacters{Ranges: tally.AlphanumericRange, Characters: []rune{'_'}},
	KeyCharacters:        tally.ValidCharacters{Ranges: tally.AlphanumericRange, Characters: []rune{'_'}},
	ValueCharacters:      tally.ValidCharacters{Ranges: tally.AlphanumericRange, Characters: []rune{'_'}},
	ReplacementCharacter: tally.DefaultReplacementCharacter,
}

type prometheusNamingScope struct{ scope tally.Scope }

// NewPrometheusNamingScope makes a scope that appends certain strings to names
// to conform to OpenMetrics naming standards. This should be used in addition
// to DefaultPrometheusSanitizeOptions.
func NewPrometheusNamingScope(scope tally.Scope) tally.Scope { return &prometheusNamingScope{scope} }

func (p *prometheusNamingScope) Counter(name string) tally.Counter {
	if !strings.HasSuffix(name, "_total") {
		name += "_total"
	}
	return p.scope.Counter(name)
}

func (p *prometheusNamingScope) Gauge(name string) tally.Gauge {
	return p.scope.Gauge(name)
}

func (p *prometheusNamingScope) Timer(name string) tally.Timer {
	if !strings.HasSuffix(name, "_seconds") {
		name += "_seconds"
	}
	return p.scope.Timer(name)
}

func (p *prometheusNamingScope) Histogram(name string, buckets tally.Buckets) tally.Histogram {
	if !strings.HasSuffix(name, "_seconds") {
		name += "_seconds"
	}
	return p.scope.Histogram(name, buckets)
}

func (p *prometheusNamingScope) Tagged(tags map[string]string) tally.Scope {
	return &prometheusNamingScope{p.scope.Tagged(tags)}
}

func (p *prometheusNamingScope) SubScope(name string) tally.Scope {
	return &prometheusNamingScope{p.scope.SubScope(name)}
}

func (p *prometheusNamingScope) Capabilities() tally.Capabilities {
	return p.scope.Capabilities()
}
