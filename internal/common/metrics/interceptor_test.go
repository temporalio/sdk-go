package metrics

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/uber-go/tally"
	"go.temporal.io/temporal-proto/serviceerror"
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
	}{
		{
			name:                 "Success",
			grpcMethod:           "/workflowservice.WorkflowService/RegisterNamespace",
			err:                  nil,
			expectedMetricName:   "RegisterNamespace",
			expectedCounterNames: []string{TemporalRequest},
		},
		{
			name:                 "GenericError",
			grpcMethod:           "/workflowservice.WorkflowService/PollForActivityTask",
			err:                  serviceerror.NewInternal("internal error"),
			expectedMetricName:   "PollForActivityTask",
			expectedCounterNames: []string{TemporalRequest, TemporalError},
		},
		{
			name:                 "InvalidRequestError",
			grpcMethod:           "/workflowservice.WorkflowService/QueryWorkflow",
			err:                  serviceerror.NewNotFound("not found"),
			expectedMetricName:   "QueryWorkflow",
			expectedCounterNames: []string{TemporalRequest, TemporalInvalidRequest},
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

			err := interceptor(context.Background(), tc.grpcMethod, nil, nil, nil, invoker)
			if tc.err == nil {
				assert.NoError(err)
			} else {
				assert.Error(err)
			}

			// Important: close before assert.
			assert.NoError(closer.Close())
			assertMetrics(assert, reporter, tc.expectedMetricName, tally.DefaultSeparator, tc.expectedCounterNames)
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
			assertPrometheusMetrics(assert, reporter, tc.expectedMetricName, tc.expectedCounterNames)
		})
	}

}

func assertMetrics(assert *assert.Assertions, reporter *CapturingStatsReporter, methodName, separator string, counterNames []string) {
	assert.Equal(len(counterNames), len(reporter.counts))
	for _, name := range counterNames {
		counterName := TemporalMetricsPrefix + methodName + separator + name
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
	assert.Equal(TemporalMetricsPrefix+methodName+separator+TemporalLatency, reporter.timers[0].name)
}

func assertPrometheusMetrics(assert *assert.Assertions, reporter *CapturingStatsReporter, methodName string, counterNames []string) {
	assertMetrics(assert, reporter, methodName, "_", counterNames)
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
