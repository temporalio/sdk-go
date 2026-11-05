package internal

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/stretchr/testify/require"
	service "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestTokenActivityResultReportingContext(t *testing.T) {
	for _, kind := range []string{"Completed", "Failed", "Canceled"} {
		for _, cancelReport := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/live", true: "/canceled"}[cancelReport], func(t *testing.T) {
				svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
				parent, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				parentDeadline, _ := parent.Deadline()
				var reportContext context.Context
				var frozen proto.Message
				calls := 0
				attempt := func(ctx context.Context, method string, q proto.Message, reply any, options ...grpc.CallOption) error {
					reportContext = ctx
					deadline, ok := ctx.Deadline()
					require.True(t, ok)
					require.Equal(t, parentDeadline, deadline)
					frozen = proto.Clone(q)
					invoke := func(ctx context.Context, m string, req, resp any, c *grpc.ClientConn, opts ...grpc.CallOption) error {
						return grpc_retry.UnaryClientInterceptor()(ctx, m, req, resp, c, func(ctx context.Context, _ string, req, resp any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
							calls++
							require.Same(t, q, req)
							require.True(t, proto.Equal(frozen, req.(proto.Message)))
							if calls == 1 {
								if cancelReport {
									cancel()
								}
								return status.Error(codes.DeadlineExceeded, "remote timeout")
							}
							return nil
						}, opts...)
					}
					return retry.NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, method, q, reply, nil, invoke, options...)
				}
				var request any
				switch kind {
				case "Completed":
					request = &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}
					svc.EXPECT().RespondActivityTaskCompleted(gomock.Any(), request, gomock.Any()).DoAndReturn(func(ctx context.Context, q *service.RespondActivityTaskCompletedRequest, o ...grpc.CallOption) (*service.RespondActivityTaskCompletedResponse, error) {
						return &service.RespondActivityTaskCompletedResponse{}, attempt(ctx, service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, q, &service.RespondActivityTaskCompletedResponse{}, o...)
					})
				case "Failed":
					request = &service.RespondActivityTaskFailedRequest{TaskToken: []byte("owned-token")}
					svc.EXPECT().RespondActivityTaskFailed(gomock.Any(), request, gomock.Any()).DoAndReturn(func(ctx context.Context, q *service.RespondActivityTaskFailedRequest, o ...grpc.CallOption) (*service.RespondActivityTaskFailedResponse, error) {
						return &service.RespondActivityTaskFailedResponse{}, attempt(ctx, service.WorkflowService_RespondActivityTaskFailed_FullMethodName, q, &service.RespondActivityTaskFailedResponse{}, o...)
					})
				case "Canceled":
					request = &service.RespondActivityTaskCanceledRequest{TaskToken: []byte("owned-token")}
					svc.EXPECT().RespondActivityTaskCanceled(gomock.Any(), request, gomock.Any()).DoAndReturn(func(ctx context.Context, q *service.RespondActivityTaskCanceledRequest, o ...grpc.CallOption) (*service.RespondActivityTaskCanceledResponse, error) {
						return &service.RespondActivityTaskCanceledResponse{}, attempt(ctx, service.WorkflowService_RespondActivityTaskCanceled_FullMethodName, q, &service.RespondActivityTaskCanceledResponse{}, o...)
					})
				}
				err := reportActivityComplete(parent, svc, request, metrics.NopHandler)
				if cancelReport {
					require.Error(t, err)
					require.Equal(t, 1, calls)
				} else {
					require.NoError(t, err)
					require.Equal(t, 2, calls)
				}
				require.ErrorIs(t, reportContext.Err(), context.Canceled)
			})
		}
	}
}

func TestTokenActivityResultReportingBackgroundBound(t *testing.T) {
	svc := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	parent := t.Context()
	_, hasDeadline := parent.Deadline()
	require.False(t, hasDeadline)
	request := &service.RespondActivityTaskCompletedRequest{TaskToken: []byte("owned-token")}
	before := time.Now()
	calls := 0
	var reportContext context.Context
	var originalDeadline time.Time
	svc.EXPECT().RespondActivityTaskCompleted(gomock.Any(), request, gomock.Any()).DoAndReturn(func(ctx context.Context, q *service.RespondActivityTaskCompletedRequest, options ...grpc.CallOption) (*service.RespondActivityTaskCompletedResponse, error) {
		reportContext = ctx
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.WithinRange(t, deadline, before.Add(defaultRPCTimeout), time.Now().Add(defaultRPCTimeout))
		originalDeadline = deadline
		invoke := func(ctx context.Context, m string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return grpc_retry.UnaryClientInterceptor()(ctx, m, req, reply, cc, func(ctx context.Context, _ string, req, reply any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				calls++
				require.Same(t, request, req)
				current, ok := ctx.Deadline()
				require.True(t, ok)
				require.Equal(t, originalDeadline, current)
				if calls == 1 {
					return status.Error(codes.DeadlineExceeded, "remote timeout")
				}
				return nil
			}, opts...)
		}
		err := retry.NewRetryOptionsInterceptor(&atomic.Bool{})(ctx, service.WorkflowService_RespondActivityTaskCompleted_FullMethodName, q, &service.RespondActivityTaskCompletedResponse{}, nil, invoke, options...)
		return &service.RespondActivityTaskCompletedResponse{}, err
	})
	require.NoError(t, reportActivityComplete(parent, svc, request, metrics.NopHandler))
	require.Equal(t, 2, calls)
	require.ErrorIs(t, reportContext.Err(), context.Canceled)
}
