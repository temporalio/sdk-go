// Copyright (c) 2017 Uber Technologies, Inc.
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

package cadence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/cadence/mocks"
)

func TestActivityHeartbeat(t *testing.T) {
	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
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
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
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
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
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
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, s.NewEntityNotExistsError()).Once()

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(t, ctx.Err(), context.Canceled)
}

func TestActivityHeartbeat_SuppressContinousInvokes(t *testing.T) {
	service := new(mocks.TChanWorkflowService)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	// Multiple calls but only one call is made.
	service.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Once()
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker.Close()
	service.AssertExpectations(t)

	// No HB timeout configured.
	service2 := new(mocks.TChanWorkflowService)
	invoker2 := newServiceInvoker([]byte("task-token"), "identity", service2, cancel, 0)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker2,
		logger:         getLogger()})
	service2.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Once()
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker2.Close()
	service2.AssertExpectations(t)

	// simulate batch picks before expiry.
	waitCh := make(chan struct{})
	service3 := new(mocks.TChanWorkflowService)
	invoker3 := newServiceInvoker([]byte("task-token"), "identity", service3, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker3,
		logger:         getLogger()})
	service3.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Once()
	service3.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Run(func(arg mock.Arguments) {
		request := arg.Get(1).(*s.RecordActivityTaskHeartbeatRequest)
		ev := EncodedValues(request.GetDetails())
		var progress string
		err := ev.Get(&progress)
		if err != nil {
			panic(err)
		}
		require.Equal(t, "testDetails-expected", progress)
		waitCh <- struct{}{}
	}).Once()

	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails2")
	RecordActivityHeartbeat(ctx, "testDetails3")
	RecordActivityHeartbeat(ctx, "testDetails-expected")
	<-waitCh
	invoker3.Close()
	service3.AssertExpectations(t)

	// simulate batch picks before expiry, with out any progress specified.
	waitCh2 := make(chan struct{})
	service4 := new(mocks.TChanWorkflowService)
	invoker4 := newServiceInvoker([]byte("task-token"), "identity", service4, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker4,
		logger:         getLogger()})
	service4.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Once()
	service4.On("RecordActivityTaskHeartbeat", mock.Anything, mock.Anything).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Run(func(arg mock.Arguments) {
		request := arg.Get(1).(*s.RecordActivityTaskHeartbeatRequest)
		require.Nil(t, request.GetDetails())
		waitCh2 <- struct{}{}
	}).Once()

	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	<-waitCh2
	invoker4.Close()
	service4.AssertExpectations(t)
}
