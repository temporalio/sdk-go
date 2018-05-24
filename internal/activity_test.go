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
	"github.com/stretchr/testify/suite"
	"go.uber.org/cadence/.gen/go/cadence/workflowservicetest"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/cadence/internal/common/backoff"
	"go.uber.org/yarpc"
)

type activityTestSuite struct {
	suite.Suite
	mockCtrl *gomock.Controller
	service  *workflowservicetest.MockClient
}

func TestActivityTestSuite(t *testing.T) {
	s := new(activityTestSuite)
	suite.Run(t, s)
}

func (s *activityTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicetest.NewMockClient(s.mockCtrl)
}

func (s *activityTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

// this is the mock for yarpcCallOptions, make sure length are the same
var callOptions = []interface{}{gomock.Any(), gomock.Any(), gomock.Any()}

func (s *activityTestSuite) TestActivityHeartbeat() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{serviceInvoker: invoker})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
}

func (s *activityTestSuite) TestActivityHeartbeat_InternalError() {
	p := backoff.NewExponentialRetryPolicy(time.Millisecond)
	p.SetMaximumInterval(100 * time.Millisecond)
	p.SetExpirationInterval(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, cancel, 1)
	invoker.(*cadenceInvoker).retryPolicy = p
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(nil, &shared.InternalServiceError{}).
		Do(func(ctx context.Context, request *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			fmt.Println("MOCK RecordActivityTaskHeartbeat executed")
		}).AnyTimes()

	RecordActivityHeartbeat(ctx, "testDetails")
}

func (s *activityTestSuite) TestActivityHeartbeat_CancelRequested() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{CancelRequested: common.BoolPtr(true)}, nil).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(s.T(), ctx.Err(), context.Canceled)
}

func (s *activityTestSuite) TestActivityHeartbeat_EntityNotExist() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, cancel, 1)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, &shared.EntityNotExistsError{}).Times(1)

	RecordActivityHeartbeat(ctx, "testDetails")
	<-ctx.Done()
	require.Equal(s.T(), ctx.Err(), context.Canceled)
}

func (s *activityTestSuite) TestActivityHeartbeat_SuppressContinousInvokes() {
	ctx, cancel := context.WithCancel(context.Background())
	invoker := newServiceInvoker([]byte("task-token"), "identity", s.service, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker,
		logger:         getLogger()})

	// Multiple calls but only one call is made.
	s.service.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker.Close()

	// No HB timeout configured.
	service2 := workflowservicetest.NewMockClient(s.mockCtrl)
	invoker2 := newServiceInvoker([]byte("task-token"), "identity", service2, cancel, 0)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker2,
		logger:         getLogger()})
	service2.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	RecordActivityHeartbeat(ctx, "testDetails")
	RecordActivityHeartbeat(ctx, "testDetails")
	invoker2.Close()

	// simulate batch picks before expiry.
	waitCh := make(chan struct{})
	service3 := workflowservicetest.NewMockClient(s.mockCtrl)
	invoker3 := newServiceInvoker([]byte("task-token"), "identity", service3, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker3,
		logger:         getLogger()})
	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)

	service3.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
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
	invoker3.Close()

	// simulate batch picks before expiry, with out any progress specified.
	waitCh2 := make(chan struct{})
	service4 := workflowservicetest.NewMockClient(s.mockCtrl)
	invoker4 := newServiceInvoker([]byte("task-token"), "identity", service4, cancel, 2)
	ctx = context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		serviceInvoker: invoker4,
		logger:         getLogger()})
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).Times(1)
	service4.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), callOptions...).
		Return(&shared.RecordActivityTaskHeartbeatResponse{}, nil).
		Do(func(ctx context.Context, request *shared.RecordActivityTaskHeartbeatRequest, opts ...yarpc.CallOption) {
			require.Nil(s.T(), request.Details)
			waitCh2 <- struct{}{}
		}).Times(1)

	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	RecordActivityHeartbeat(ctx, nil)
	<-waitCh2
	invoker4.Close()
}
