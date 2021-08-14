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
	"strings"
	"time"

	"github.com/uber-go/tally"
)

type (
	requestScope struct {
		scope                        tally.Scope
		startTime                    time.Time
		isLongPoll                   bool
		longPollRequestCountMetric   string
		requestCountMetric           string
		longPollRequestLatencyMetric string
		requestLatencyMetric         string
		longPollRequestFailureMetric string
		requestFailureMetric         string
	}
)

// newRequestScope creates metric scope for a specified operation, defined by gRPC method string, isLongPoll flag and
// metric suffix. Suffix should be an empty string for individual calls and should have non-empty value for aggregated values.
func newRequestScope(scope tally.Scope, method string, isLongPoll bool, suffix string) *requestScope {
	operation := ConvertMethodToScope(method)
	subScope := getMetricsScopeForOperation(scope, operation)

	return &requestScope{
		scope:                        subScope,
		startTime:                    time.Now(),
		isLongPoll:                   isLongPoll,
		longPollRequestCountMetric:   TemporalLongRequest + suffix,
		requestCountMetric:           TemporalRequest + suffix,
		longPollRequestLatencyMetric: TemporalLongRequestLatency + suffix,
		requestLatencyMetric:         TemporalRequestLatency + suffix,
		longPollRequestFailureMetric: TemporalLongRequestFailure + suffix,
		requestFailureMetric:         TemporalRequestFailure + suffix,
	}
}

func (rs *requestScope) recordStart() {
	if rs.isLongPoll {
		rs.scope.Counter(rs.longPollRequestCountMetric).Inc(1)
	} else {
		rs.scope.Counter(rs.requestCountMetric).Inc(1)
	}
}

func (rs *requestScope) recordEnd(err error) {
	if rs.isLongPoll {
		rs.scope.Timer(rs.longPollRequestLatencyMetric).Record(time.Since(rs.startTime))
	} else {
		rs.scope.Timer(rs.requestLatencyMetric).Record(time.Since(rs.startTime))
	}

	if err != nil {
		if rs.isLongPoll {
			rs.scope.Counter(rs.longPollRequestFailureMetric).Inc(1)
		} else {
			rs.scope.Counter(rs.requestFailureMetric).Inc(1)
		}
	}
}

// ConvertMethodToScope extracts API name from the method string by truncating the prefix
func ConvertMethodToScope(method string) string {
	// method is something like "/temporal.api.workflowservice.v1.WorkflowService/RegisterNamespace"
	methodStart := strings.LastIndex(method, "/") + 1
	return method[methodStart:]
}
