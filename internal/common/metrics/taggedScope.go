// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	for i := 0; i < len(keyValuePairs); i += 2 {
		tagName := keyValuePairs[i]
		tagValue := keyValuePairs[i+1]
		key += tagName + ":" + tagValue + "-" // used to prevent collision of tagValue (map key) for different tagName
	}

	taggedScope, ok := ts.Load(key)
	if !ok {
		tagsMap := map[string]string{}
		for i := 0; i < len(keyValuePairs); i += 2 {
			tagName := keyValuePairs[i]
			tagValue := keyValuePairs[i+1]
			tagsMap[tagName] = tagValue
		}

		ts.Store(key, ts.Scope.Tagged(tagsMap))
		taggedScope, _ = ts.Load(key)
	}
	if taggedScope == nil {
		panic("metric scope cannot be tagged") // This should never happen
	}

	return taggedScope.(tally.Scope)
}
