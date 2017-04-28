package cadence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	s "github.com/uber-go/cadence-client/.gen/go/shared"
	"github.com/uber-go/cadence-client/common"
	"github.com/uber-go/cadence-client/common/backoff"
	"github.com/uber-go/cadence-client/mocks"
)

func TestActivityHeartbeat(t *testing.T) {
	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{serviceInvoker: invoker})

	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Once()

	RecordActivityHeartbeat(ctx, "testDetails")
}

func TestActivityHeartbeat_InternalError(t *testing.T) {
	p := backoff.NewExponentialRetryPolicy(time.Millisecond)
	p.SetMaximumInterval(100 * time.Millisecond)
	p.SetExpirationInterval(100 * time.Millisecond)

	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel)
	invoker.(*cadenceInvoker).retryPolicy = p
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(nil, s.NewInternalServiceError())

	RecordActivityHeartbeat(ctx, "testDetails")
}

func TestActivityHeartbeat_CancelRequested(t *testing.T) {
	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{CancelRequested: common.BoolPtr(true)}, nil).Once()

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(t, ctx.Err(), context.Canceled)
}

func TestActivityHeartbeat_EntityNotExist(t *testing.T) {
	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, s.NewEntityNotExistsError()).Once()

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(t, ctx.Err(), context.Canceled)
}
