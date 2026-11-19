package client_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRPCTimeout(t *testing.T) {
	type observedCall struct {
		method   string
		deadline time.Time
	}
	calls := make(chan observedCall, 2)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.UnaryInterceptor(
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			deadline, _ := ctx.Deadline()
			calls <- observedCall{method: info.FullMethod, deadline: deadline}
			if strings.HasSuffix(info.FullMethod, "/GetSystemInfo") {
				return &workflowservice.GetSystemInfoResponse{}, nil
			}
			return nil, status.Error(codes.InvalidArgument, "test response")
		},
	))
	workflowservice.RegisterWorkflowServiceServer(server, &workflowservice.UnimplementedWorkflowServiceServer{})
	t.Cleanup(server.Stop)
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Error(err)
		}
	}()

	assertCall := func(t *testing.T, method string, start time.Time, timeout time.Duration) {
		t.Helper()
		call := <-calls
		require.True(t, strings.HasSuffix(call.method, "/"+method), call.method)
		if timeout == 0 {
			require.True(t, call.deadline.IsZero())
		} else {
			require.WithinDuration(t, start.Add(timeout), call.deadline, time.Second)
		}
	}

	for _, tc := range []struct {
		name          string
		rpcTimeout    time.Duration
		callerTimeout time.Duration
		wantTimeout   time.Duration
	}{
		{name: "default", wantTimeout: 10 * time.Second},
		{name: "raised", rpcTimeout: time.Minute, wantTimeout: time.Minute},
		{name: "lowered", rpcTimeout: 5 * time.Second, wantTimeout: 5 * time.Second},
		{name: "shorter caller", rpcTimeout: time.Minute, callerTimeout: 20 * time.Second, wantTimeout: 20 * time.Second},
		{name: "longer caller", rpcTimeout: time.Minute, callerTimeout: 2 * time.Minute, wantTimeout: time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			c, err := client.DialContext(t.Context(), client.Options{
				HostPort: listener.Addr().String(), RPCTimeout: tc.rpcTimeout,
			})
			require.NoError(t, err)
			defer c.Close()
			assertCall(t, "GetSystemInfo", start, 5*time.Second)

			for _, call := range []struct {
				method string
				invoke func(context.Context) error
			}{
				{"SignalWorkflowExecution", func(ctx context.Context) error {
					return c.SignalWorkflow(ctx, "workflow-id", "", "signal", nil)
				}},
				{"RecordActivityTaskHeartbeat", func(ctx context.Context) error {
					return c.RecordActivityHeartbeat(ctx, []byte("task-token"))
				}},
				{"RespondActivityTaskCompleted", func(ctx context.Context) error {
					return c.CompleteActivity(ctx, []byte("task-token"), nil, nil)
				}},
				{"DeleteSchedule", func(ctx context.Context) error {
					return c.ScheduleClient().GetHandle(ctx, "schedule-id").Delete(ctx)
				}},
			} {
				t.Run(call.method, func(t *testing.T) {
					ctx := t.Context()
					if tc.callerTimeout != 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, tc.callerTimeout)
						defer cancel()
					}
					start := time.Now()
					require.Error(t, call.invoke(ctx))
					assertCall(t, call.method, start, tc.wantTimeout)
				})
			}

			start = time.Now()
			iter := c.GetWorkflowHistory(t.Context(), "workflow-id", "", true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			require.True(t, iter.HasNext())
			_, err = iter.Next()
			require.Error(t, err)
			assertCall(t, "GetWorkflowExecutionHistory", start, 65*time.Second)

			start = time.Now()
			_, err = c.WorkflowService().DescribeNamespace(t.Context(), &workflowservice.DescribeNamespaceRequest{})
			require.Error(t, err)
			assertCall(t, "DescribeNamespace", start, 0)
		})
	}

	t.Run("shared connection", func(t *testing.T) {
		start := time.Now()
		parent, err := client.DialContext(t.Context(), client.Options{
			HostPort: listener.Addr().String(), RPCTimeout: time.Minute,
		})
		require.NoError(t, err)
		defer parent.Close()
		assertCall(t, "GetSystemInfo", start, 5*time.Second)
		for _, timeout := range []time.Duration{30 * time.Second, 0} {
			child, err := client.NewClientFromExistingWithContext(t.Context(), parent, client.Options{RPCTimeout: timeout})
			require.NoError(t, err)
			defer child.Close()
			start = time.Now()
			require.Error(t, child.SignalWorkflow(t.Context(), "workflow-id", "", "signal", nil))
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			assertCall(t, "SignalWorkflowExecution", start, timeout)
		}
		start = time.Now()
		require.Error(t, parent.SignalWorkflow(t.Context(), "workflow-id", "", "signal", nil))
		assertCall(t, "SignalWorkflowExecution", start, time.Minute)
	})

	t.Run("namespace client", func(t *testing.T) {
		c, err := client.NewNamespaceClient(client.Options{HostPort: listener.Addr().String(), RPCTimeout: time.Minute})
		require.NoError(t, err)
		defer c.Close()
		start := time.Now()
		_, err = c.Describe(t.Context(), "default")
		require.Error(t, err)
		assertCall(t, "DescribeNamespace", start, time.Minute)
	})

	t.Run("lazy client", func(t *testing.T) {
		c, err := client.NewLazyClient(client.Options{HostPort: listener.Addr().String(), RPCTimeout: time.Minute})
		require.NoError(t, err)
		defer c.Close()
		start := time.Now()
		require.Error(t, c.SignalWorkflow(t.Context(), "workflow-id", "", "signal", nil))
		assertCall(t, "GetSystemInfo", start, 5*time.Second)
		assertCall(t, "SignalWorkflowExecution", start, time.Minute)
	})
}

func TestRPCTimeoutValidation(t *testing.T) {
	options := client.Options{RPCTimeout: -time.Second}
	_, err := client.DialContext(t.Context(), options)
	require.ErrorContains(t, err, "RPCTimeout must not be negative")
	_, err = client.NewLazyClient(options)
	require.ErrorContains(t, err, "RPCTimeout must not be negative")
	_, err = client.NewNamespaceClient(options)
	require.ErrorContains(t, err, "RPCTimeout must not be negative")
}
