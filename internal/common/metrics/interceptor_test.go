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
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc"
)

func TestMetricsInterceptor(t *testing.T) {
	assert := assert.New(t)

	isReplay := false

	testCases := []struct {
		name                 string
		grpcMethod           string
		err                  error
		expectedMetricName   string
		expectedCounterNames []string
		isLongPoll           bool
	}{
		{
			name:                 "Success",
			grpcMethod:           "/workflowservice.WorkflowService/RegisterNamespace",
			err:                  nil,
			expectedMetricName:   "RegisterNamespace",
			expectedCounterNames: []string{TemporalRequest},
		},
		{
			name:                 "GenericErrorLongPoll",
			grpcMethod:           "/workflowservice.WorkflowService/PollActivityTaskQueue",
			err:                  serviceerror.NewInternal("internal error"),
			expectedMetricName:   "PollActivityTaskQueue",
			expectedCounterNames: []string{TemporalLongRequest, TemporalLongRequestFailure},
			isLongPoll:           true,
		},
		{
			name:                 "InvalidRequestError",
			grpcMethod:           "/workflowservice.WorkflowService/QueryWorkflow",
			err:                  serviceerror.NewNotFound("not found"),
			expectedMetricName:   "QueryWorkflow",
			expectedCounterNames: []string{TemporalRequest, TemporalRequestFailure},
		},
	}

	// Normal metrics scope.
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scope, closer, reporter := NewMetricsScope(&isReplay)
			interceptor := NewScopeInterceptor(scope)

			invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return tc.err
			}

			ctx := context.WithValue(context.Background(), LongPollContextKey, tc.isLongPoll)

			err := interceptor(ctx, tc.grpcMethod, nil, nil, nil, invoker)
			if tc.err == nil {
				assert.NoError(err)
			} else {
				assert.Error(err)
			}

			// Important: close before assert.
			assert.NoError(closer.Close())
			assertMetrics(assert, reporter, tc.expectedCounterNames)
		})
	}

	// Prometheus metrics scope
	for _, tc := range testCases {
		t.Run(tc.name+"_Prometheus", func(t *testing.T) {
			t.Parallel()
			scope, closer, reporter := newPrometheusScope(&isReplay)
			interceptor := NewScopeInterceptor(scope)

			invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return tc.err
			}

			err := interceptor(context.Background(), tc.grpcMethod, nil, nil, nil, invoker)
			if tc.err == nil {
				assert.NoError(err)
			} else {
				assert.Error(err)
			}

			// Important: close before assert.
			assert.NoError(closer.Close())
			assertPrometheusMetrics(assert, reporter, tc.expectedCounterNames)
		})
	}

}

func assertMetrics(assert *assert.Assertions, reporter *CapturingStatsReporter, counterNames []string) {
	assert.Equal(len(counterNames), len(reporter.counts))
	for _, counterName := range counterNames {
		find := false
		// counters are not in order
		for _, counter := range reporter.counts {
			if counterName == counter.name {
				find = true
				break
			}
		}
		assert.True(find)
	}
	assert.Equal(1, len(reporter.timers))
	assert.Equal(TemporalRequestLatency, reporter.timers[0].name)
}

func assertPrometheusMetrics(assert *assert.Assertions, reporter *CapturingStatsReporter, counterNames []string) {
	assertMetrics(assert, reporter, counterNames)
}

func newPrometheusScope(isReplay *bool) (tally.Scope, io.Closer, *CapturingStatsReporter) {
	reporter := &CapturingStatsReporter{}
	opts := tally.ScopeOptions{
		Reporter:  reporter,
		Separator: "_",
	}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &realClock{}), closer, reporter
}
