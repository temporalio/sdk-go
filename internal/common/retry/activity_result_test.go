package retry

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/serviceerror"
	service "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type resultReportServer struct {
	service.UnimplementedWorkflowServiceServer
	mu          sync.Mutex
	calls       []proto.Message
	firstError  error
	secondError error
	always      bool
}

func (s *resultReportServer) report(q proto.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, proto.Clone(q))
	if s.always || len(s.calls) == 1 {
		return s.firstError
	}
	return s.secondError
}
func (s *resultReportServer) RespondActivityTaskCompleted(_ context.Context, q *service.RespondActivityTaskCompletedRequest) (*service.RespondActivityTaskCompletedResponse, error) {
	return &service.RespondActivityTaskCompletedResponse{}, s.report(q)
}
func (s *resultReportServer) RespondActivityTaskFailed(_ context.Context, q *service.RespondActivityTaskFailedRequest) (*service.RespondActivityTaskFailedResponse, error) {
	return &service.RespondActivityTaskFailedResponse{}, s.report(q)
}
func (s *resultReportServer) RespondActivityTaskCanceled(_ context.Context, q *service.RespondActivityTaskCanceledRequest) (*service.RespondActivityTaskCanceledResponse, error) {
	return &service.RespondActivityTaskCanceledResponse{}, s.report(q)
}

func resultReportConnection(t *testing.T, s *resultReportServer) *grpc.ClientConn {
	t.Helper()
	l := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service.RegisterWorkflowServiceServer(server, s)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(l) }()
	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, l.Close())
		select {
		case err := <-serveErr:
			if err != nil {
				require.ErrorIs(t, err, grpc.ErrServerStopped)
			}
		case <-time.After(5 * time.Second):
			t.Error("gRPC test server did not stop")
		}
	})
	c, e := grpc.NewClient("passthrough:///result-report", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }), grpc.WithChainUnaryInterceptor(NewRetryOptionsInterceptor(&atomic.Bool{}), grpc_retry.UnaryClientInterceptor()))
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}
func resultRetryContext(ctx context.Context) context.Context {
	rc := NewGrpcRetryConfig(time.Millisecond)
	rc.SetJitter(0)
	rc.SetRetryActivityTaskResultDeadlineExceeded()
	return context.WithValue(ctx, ConfigKey, rc)
}
func TestActivityResultRemoteDeadline(t *testing.T) {
	for _, kind := range []string{"Completed", "Failed", "Canceled"} {
		t.Run(kind, func(t *testing.T) {
			s := &resultReportServer{firstError: status.Error(codes.DeadlineExceeded, "remote timeout")}
			c := resultReportConnection(t, s)
			ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), time.Second)
			defer cancel()
			var req, reply proto.Message
			switch kind {
			case "Completed":
				req = &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token"), Identity: "original-worker", Namespace: "result-namespace", Result: &commonpb.Payloads{Payloads: []*commonpb.Payload{{Metadata: map[string][]byte{"encoding": []byte("test-result")}, Data: []byte("completed-result")}}}}
				reply = &service.RespondActivityTaskCompletedResponse{}
			case "Failed":
				req = &service.RespondActivityTaskFailedRequest{TaskToken: []byte("owned-token"), Identity: "original-worker", Namespace: "result-namespace", Failure: &failurepb.Failure{Message: "failed-outcome", Source: "test-worker", FailureInfo: &failurepb.Failure_ApplicationFailureInfo{ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{Type: "result-failure", NonRetryable: true}}}, LastHeartbeatDetails: &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("last-heartbeat")}}}}
				reply = &service.RespondActivityTaskFailedResponse{}
			case "Canceled":
				req = &service.RespondActivityTaskCanceledRequest{TaskToken: []byte("owned-token"), Identity: "original-worker", Namespace: "result-namespace", Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("canceled-details")}}}}
				reply = &service.RespondActivityTaskCanceledResponse{}
			}
			frozen := proto.Clone(req)
			require.NoError(t, c.Invoke(ctx, "/temporal.api.workflowservice.v1.WorkflowService/RespondActivityTask"+kind, req, reply))
			require.NoError(t, ctx.Err())
			require.True(t, proto.Equal(req, frozen))
			s.mu.Lock()
			defer s.mu.Unlock()
			require.Len(t, s.calls, 2)
			for _, q := range s.calls {
				require.True(t, proto.Equal(frozen, q))
			}
		})
	}
}
func TestActivityResultStrictErrors(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.PermissionDenied, codes.Unauthenticated, codes.InvalidArgument, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			s := &resultReportServer{firstError: status.Error(code, "rejected")}
			c := resultReportConnection(t, s)
			ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), time.Second)
			defer cancel()
			_, err := service.NewWorkflowServiceClient(c).RespondActivityTaskCompleted(ctx, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")})
			require.Equal(t, code, status.Code(err))
			s.mu.Lock()
			defer s.mu.Unlock()
			require.Len(t, s.calls, 1)
		})
	}
}
func TestActivityResultPersistentDeadlineBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), 80*time.Millisecond)
		defer cancel()
		calls := 0
		invoke := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return grpc_retry.UnaryClientInterceptor()(ctx, method, req, reply, cc, func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
				calls++
				return status.Error(codes.DeadlineExceeded, "remote timeout")
			}, opts...)
		}
		err := NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}, nil, nil, invoke)
		require.Equal(t, codes.DeadlineExceeded, status.Code(err))
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
		require.Greater(t, calls, 1)
	})
}

func TestActivityResultRetryScope(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		request      any
		configured   bool
	}{
		{"byID", service.WorkflowService_RespondActivityTaskCompletedById_FullMethodName, &service.RespondActivityTaskCompletedByIdRequest{WorkflowId: "workflow", ActivityId: "activity"}, true},
		{"emptyToken", service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, &service.RespondActivityTaskCompletedRequest{}, true},
		{"otherRPC", service.WorkflowService_RecordActivityTaskHeartbeat_FullMethodName, &service.RecordActivityTaskHeartbeatRequest{TaskToken: []byte("owned-token")}, true},
		{"wrongRequest", service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, &service.RespondActivityTaskFailedRequest{TaskToken: []byte("owned-token")}, true},
		{"unconfigured", service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if tc.configured {
				ctx = resultRetryContext(ctx)
			}
			calls := 0
			invoke := func(ctx context.Context, m string, q, r any, c *grpc.ClientConn, opts ...grpc.CallOption) error {
				return grpc_retry.UnaryClientInterceptor()(ctx, m, q, r, c, func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
					calls++
					return status.Error(codes.DeadlineExceeded, "remote timeout")
				}, opts...)
			}
			err := NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, tc.method, tc.request, nil, nil, invoke)
			require.Equal(t, codes.DeadlineExceeded, status.Code(err))
			require.Equal(t, 1, calls)
		})
	}
}
func TestActivityResultLocalContextErrors(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)} {
		t.Run(err.Error(), func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), time.Second)
			defer cancel()
			invoke := func(ctx context.Context, m string, q, r any, c *grpc.ClientConn, opts ...grpc.CallOption) error {
				return grpc_retry.UnaryClientInterceptor()(ctx, m, q, r, c, func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
					calls++
					return err
				}, opts...)
			}
			got := NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}, nil, nil, invoke)
			require.ErrorIs(t, got, err)
			require.Equal(t, 1, calls)
		})
	}
}
func TestActivityResultTypedDeadline(t *testing.T) {
	calls := 0
	ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), time.Second)
	defer cancel()
	original := &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}
	invoke := func(ctx context.Context, m string, q, r any, c *grpc.ClientConn, opts ...grpc.CallOption) error {
		return grpc_retry.UnaryClientInterceptor()(ctx, m, q, r, c, func(_ context.Context, _ string, q, r any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			calls++
			require.Same(t, original, q)
			require.True(t, bytes.Equal(original.TaskToken, []byte("owned-token")))
			if calls == 1 {
				return serviceerror.NewDeadlineExceeded("remote timeout")
			}
			return nil
		}, opts...)
	}
	require.NoError(t, NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, original, nil, nil, invoke))
	require.Equal(t, 2, calls)
}

func TestActivityResultDefaultPolicyUnchanged(t *testing.T) {
	s := &resultReportServer{firstError: status.Error(codes.DeadlineExceeded, "remote timeout")}
	c := resultReportConnection(t, s)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, ConfigKey, NewGrpcRetryConfig(time.Millisecond))
	_, err := service.NewWorkflowServiceClient(c).RespondActivityTaskCompleted(ctx, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.calls, 1)
}

func TestActivityResultRetryNotFoundStaysError(t *testing.T) {
	s := &resultReportServer{firstError: status.Error(codes.DeadlineExceeded, "remote timeout"), secondError: status.Error(codes.NotFound, "obsolete task")}
	c := resultReportConnection(t, s)
	ctx, cancel := context.WithTimeout(resultRetryContext(t.Context()), time.Second)
	defer cancel()
	_, err := service.NewWorkflowServiceClient(c).RespondActivityTaskCompleted(ctx, &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")})
	require.Equal(t, codes.NotFound, status.Code(err))
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.calls, 2)
}

func TestActivityResultExhaustedContext(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			s := &resultReportServer{firstError: status.Error(codes.DeadlineExceeded, "remote timeout")}
			c := resultReportConnection(t, s)
			var ctx context.Context
			var cancel context.CancelFunc
			if expired {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			} else {
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			defer cancel()
			_, err := service.NewWorkflowServiceClient(c).RespondActivityTaskCompleted(resultRetryContext(ctx), &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")})
			if expired {
				require.Equal(t, codes.DeadlineExceeded, status.Code(err))
			} else {
				require.Equal(t, codes.Canceled, status.Code(err))
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			require.Empty(t, s.calls)
		})
	}
}
