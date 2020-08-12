package metrics

import (
	"sync"

	"github.com/uber-go/tally"
)

type (
	// TaggedScope provides metricScope with tags
	TaggedScope struct {
		tally.Scope
		*sync.Map
	}
)

// NewTaggedScope create a new TaggedScope
func NewTaggedScope(scope tally.Scope) *TaggedScope {
	if scope == nil {
		scope = tally.NoopScope
	}
	return &TaggedScope{Scope: scope, Map: &sync.Map{}}
}

// GetTaggedScope return a scope with one or multiple tags,
// input should be key value pairs like: GetTaggedScope(scope, tag1, val1, tag2, val2).
func (ts *TaggedScope) GetTaggedScope(keyValuePairs ...string) tally.Scope {
	if ts == nil {
		return nil
	}

	if len(keyValuePairs)%2 != 0 {
		panic("GetTaggedScope key value are not in pairs")
	}
	if ts.Map == nil {
		ts.Map = &sync.Map{}
	}

	key := ""
	tagsMap := map[string]string{}
	for i := 0; i < len(keyValuePairs); i += 2 {
		tagName := keyValuePairs[i]
		tagValue := keyValuePairs[i+1]
		key = key + tagName + ":" + tagValue + "-" // used to prevent collision of tagValue (map key) for different tagName
		tagsMap[tagName] = tagValue
	}

	taggedScope, ok := ts.Load(key)
	if !ok {
		ts.Store(key, ts.Scope.Tagged(tagsMap))
		taggedScope, _ = ts.Load(key)
	}
	if taggedScope == nil {
		panic("metric scope cannot be tagged") // This should never happen
	}

	return taggedScope.(tally.Scope)
}
