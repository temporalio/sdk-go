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
