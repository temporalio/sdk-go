package internal

import (
	"context"
	"testing"

	"github.com/gogo/status"
	"github.com/stretchr/testify/require"
	"go.temporal.io/temporal-proto/failure"
	"go.temporal.io/temporal-proto/serviceerror"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestErrorWrapper_SimpleError(t *testing.T) {
	require := require.New(t)

	svcerr := errorInterceptor(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return status.Error(codes.NotFound, "Something not found")
		})

	require.IsType(&serviceerror.NotFound{}, svcerr)
	require.Equal("Something not found", svcerr.Error())
}

func TestErrorWrapper_ErrorWithFailure(t *testing.T) {
	require := require.New(t)

	svcerr := errorInterceptor(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			st, _ := status.New(codes.AlreadyExists, "Something started").WithDetails(&failure.WorkflowExecutionAlreadyStarted{
				StartRequestId: "srId",
				RunId:          "rId",
			})

			return st.Err()
		})

	require.IsType(&serviceerror.WorkflowExecutionAlreadyStarted{}, svcerr)
	require.Equal("Something started", svcerr.Error())
	weasErr := svcerr.(*serviceerror.WorkflowExecutionAlreadyStarted)
	require.Equal("rId", weasErr.RunId)
	require.Equal("srId", weasErr.StartRequestId)
}
