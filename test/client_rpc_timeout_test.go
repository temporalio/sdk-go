package test_test

import (
	"context"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
)

func (ts *IntegrationTestSuite) TestRPCTimeout() {
	for _, tc := range []struct {
		name          string
		callerTimeout time.Duration
		wantTimeout   time.Duration
	}{
		{name: "configured cap", wantTimeout: time.Minute},
		{name: "shorter caller deadline", callerTimeout: 20 * time.Second, wantTimeout: 20 * time.Second},
	} {
		ts.Run(tc.name, func() {
			t := ts.T()
			deadlines := make(chan time.Time, 1)
			completionTimeouts := make(chan time.Duration, 1)
			c, err := ts.newIntegrationTestClient(func(options *client.Options) {
				options.RPCTimeout = time.Minute
				options.ConnectionOptions.DialOptions = append(options.ConnectionOptions.DialOptions,
					grpc.WithChainUnaryInterceptor(func(
						ctx context.Context, method string, req, reply any, cc *grpc.ClientConn,
						invoker grpc.UnaryInvoker, opts ...grpc.CallOption,
					) error {
						if strings.HasSuffix(method, "/StartWorkflowExecution") {
							deadline, _ := ctx.Deadline()
							select {
							case deadlines <- deadline:
							default:
							}
						} else if strings.HasSuffix(method, "/RespondWorkflowTaskCompleted") {
							deadline, _ := ctx.Deadline()
							select {
							case completionTimeouts <- time.Until(deadline):
							default:
							}
						}
						return invoker(ctx, method, req, reply, cc, opts...)
					}))
			})
			require.NoError(t, err)
			defer c.Close()
			options := ts.startWorkflowOptions("test-rpc-timeout-" + tc.name)
			options.TaskQueue += "-rpc-timeout"
			w := worker.New(c, options.TaskQueue, worker.Options{})
			w.RegisterWorkflow(ts.workflows.SimplestWorkflow)
			require.NoError(t, w.Start())
			defer w.Stop()
			ctx := t.Context()
			if tc.callerTimeout != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.callerTimeout)
				defer cancel()
			}
			start := time.Now()
			run, err := c.ExecuteWorkflow(ctx, options, ts.workflows.SimplestWorkflow)
			require.NoError(t, err)
			select {
			case deadline := <-deadlines:
				require.WithinDuration(t, start.Add(tc.wantTimeout), deadline, time.Second)
			default:
				t.Fatal("StartWorkflowExecution was not observed")
			}
			require.NoError(t, run.Get(ctx, nil))
			select {
			case timeout := <-completionTimeouts:
				require.InDelta(t, time.Minute, timeout, float64(time.Second))
			default:
				t.Fatal("RespondWorkflowTaskCompleted was not observed")
			}
		})
	}
}
