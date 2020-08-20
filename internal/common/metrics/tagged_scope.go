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
	"github.com/uber-go/tally"
)

type (
	// TaggedScope provides metricScope with tags
	TaggedScope struct {
		tally.Scope
	}
)

// NewTaggedScope create a new TaggedScope
func NewTaggedScope(scope tally.Scope) *TaggedScope {
	if scope == nil {
		scope = tally.NoopScope
	}
	return &TaggedScope{Scope: scope}
}

// GetTaggedScope return a scope with one or multiple tags,
// input should be key value pairs like: GetTaggedScope(tag1, val1, tag2, val2).
func (ts *TaggedScope) GetTaggedScope(keyValuePairs ...string) tally.Scope {
	if ts == nil {
		return nil
	}

	if len(keyValuePairs)%2 != 0 {
		panic("GetTaggedScope key value are not in pairs")
	}

	tagsMap := map[string]string{}
	for i := 0; i < len(keyValuePairs); i += 2 {
		tagName := keyValuePairs[i]
		tagValue := keyValuePairs[i+1]
		tagsMap[tagName] = tagValue
	}

	return ts.Scope.Tagged(tagsMap)
}
