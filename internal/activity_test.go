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

package internal

import (
	"context"
	"testing"
	"time"

	"fmt"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/backoff"
	"go.uber.org/yarpc"
)

// this is the mock for yarpcCallOptions, make sure length are the same
var callOptions = []interface{}{gomock.Any(), gomock.Any(), gomock.Any()}

func TestActivityHeartbeat(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{serviceInvoker: invoker})

	service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
}

func TestActivityHeartbeat_InternalError(t *testing.T) {
	p := backoff.NewExponentialRetryPolicy(time.Millisecond)
	p.SetMaximumInterval(100 * time.Millisecond)
	p.SetExpirationInterval(100 * time.Millisecond)

	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
	invoker.(*cadenceInvoker).retryPolicy = p
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(nil, &s.InternalServiceError{}).
		Do(func(ctx context.Context, request *s.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			fmt.Println("MOCK RecordActivityTaskHeartbeat executed")
		}).AnyTimes()

	RecordActivityHeartbeat(ctx, "testDetails")
}

func TestActivityHeartbeat_CancelRequested(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{CancelRequested: common.BoolPtr(true)}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(t, ctx.Err(), context.Canceled)
}

func TestActivityHeartbeat_EntityNotExist(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, &s.EntityNotExistsError{}).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(t, ctx.Err(), context.Canceled)
}

func TestActivityHeartbeat_SuppressContinousInvokes(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	service := workflowservicetest.NewMockClient(mockCtrl)

	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", service, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	// Multiple calls but only one call is made.
	service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker.Close()

	// No HB timeout configured.
	service2 := workflowservicetest.NewMockClient(mockCtrl)
	invoker2 := newServiceInvoker([]byte("task-token"), "identity", service2, cancel, 0)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker2,
		logger:         getLogger()})
	service2.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker2.Close()

	// simulate batch picks before expiry.
	waitCh := make(chan struct{})
	service3 := workflowservicetest.NewMockClient(mockCtrl)
	invoker3 := newServiceInvoker([]byte("task-token"), "identity", service3, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker3,
		logger:         getLogger()})
	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *s.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			ev := EncodedValues(request.Details)
			var progress string
			err := ev.Get(&progress)
			if err != nil {
				panic(err)
			}
			require.Equal(t, "testDetails-expected", progress)
			waitCh <- struct{}{}
		}).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails2")
	RecordActivityHeartbeat(ctx, "testDetails3")
	RecordActivityHeartbeat(ctx, "testDetails-expected")
	<-waitCh
	invoker3.Close()

	// simulate batch picks before expiry, with out any progress specified.
	waitCh2 := make(chan struct{})
	service4 := workflowservicetest.NewMockClient(mockCtrl)
	invoker4 := newServiceInvoker([]byte("task-token"), "identity", service4, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker4,
		logger:         getLogger()})
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&s.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *s.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			require.Nil(t, request.Details)
			waitCh2 <- struct{}{}
		}).Times(1)

	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	<-waitCh2
	invoker4.Close()
}
