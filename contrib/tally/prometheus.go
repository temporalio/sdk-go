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
