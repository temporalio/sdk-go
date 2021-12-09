// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/grpc"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
)

type activityTestSuite struct {
	suite.Suite
	mockCtrl  *gomock.Controller
	service   *workflowservicemock.MockWorkflowServiceClient
	namespace string
}

func TestActivityTestSuite(t *testing.T) {
	s := new(activityTestSuite)
	suite.Run(t, s)
}

func (s *activityTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	s.namespace = "default"
}

func (s *activityTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *activityTestSuite) TestActivityHeartbeat() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		1*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{serviceInvoker: invoker})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
}

func (s *activityTestSuite) TestActivityHeartbeat_InternalError() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		1*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(nil, serviceerror.NewInternal("")).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			fmt.Println("MOCK RecordActivityTaskHeartbeat executed")
		}).AnyTimes()

	RecordActivityHeartbeat(ctx, "testDetails")
}

func (s *activityTestSuite) TestActivityHeartbeat_CancelRequested() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		1*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{CancelRequested: true}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(s.T(), ctx.Err(), context.Canceled)
}

func (s *activityTestSuite) TestActivityHeartbeat_EntityNotExist() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		1*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, serviceerror.NewNotFound("")).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(s.T(), ctx.Err(), context.Canceled)
}

func (s *activityTestSuite) TestActivityHeartbeat_SuppressContinousInvokes() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		2*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	// Multiple calls but only one call is made.
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker.Close(ctx, false)

	// High HB timeout configured.
	service2 := workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	invoker2 := newServiceInvoker([]byte("task-token"), "identity", service2, metrics.NopHandler, cancel,
		20*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker2,
		logger:         getLogger()})
	service2.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker2.Close(ctx, false)

	// simulate batch picks before expiry.
	waitCh := make(chan struct{})
	service3 := workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	invoker3 := newServiceInvoker([]byte("task-token"), "identity", service3, metrics.NopHandler, cancel,
		2*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker3,
		logger:         getLogger()})
	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			ev := newEncodedValues(request.Details, nil)
			var progress string
			err := ev.Get(&progress)
			if err != nil {
				panic(err)
			}
			require.Equal(s.T(), "testDetails-expected", progress)
			waitCh <- struct{}{}
		}).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails2")
	RecordActivityHeartbeat(ctx, "testDetails3")
	RecordActivityHeartbeat(ctx, "testDetails-expected")
	<-waitCh
	invoker3.Close(ctx, false)

	// simulate batch picks before expiry, without any progress specified.
	waitCh2 := make(chan struct{})
	service4 := workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
	invoker4 := newServiceInvoker([]byte("task-token"), "identity", service4, metrics.NopHandler, cancel,
		2*time.Second, make(chan struct{}), s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{
		serviceInvoker: invoker4,
		logger:         getLogger()})
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			require.Nil(s.T(), request.Details)
			waitCh2 <- struct{}{}
		}).Times(1)

	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	<-waitCh2
	invoker4.Close(ctx, false)
}

func (s *activityTestSuite) TestActivityHeartbeat_WorkerStop() {
	ctx, cancel := context.WithCancel(context.Background())
	workerStopChannel := make(chan struct{})
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, metrics.NopHandler, cancel,
		5*time.Second, workerStopChannel, s.namespace)
	ctx, _ = newActivityContext(ctx, nil, &activityEnvironment{serviceInvoker: invoker})

	heartBeatDetail := "testDetails"
	waitCh := make(chan struct{}, 1)
	waitCh <- struct{}{}
	waitC2 := make(chan struct{}, 1)
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *workflowservice.RecordActivityTaskHeartbeatRequest, opts ...grpc.CallOption) {
			if _, ok := <-waitCh; ok {
				close(waitCh)
				return
			}
			close(waitC2)
		}).Times(2)
	RecordActivityHeartbeat(ctx, heartBeatDetail)
	RecordActivityHeartbeat(ctx, "testDetails")
	close(workerStopChannel)
	<-waitC2
}

func (s *activityTestSuite) TestGetWorkerStopChannel() {
	ch := make(chan struct{}, 1)
	ctx, _ := newActivityContext(context.Background(), nil, &activityEnvironment{workerStopChannel: ch})
	channel := GetWorkerStopChannel(ctx)
	s.NotNil(channel)
}
