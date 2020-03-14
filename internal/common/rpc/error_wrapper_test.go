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

package rpc

import (
	"testing"

	"github.com/gogo/status"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/temporal-proto/failure"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.temporal.io/temporal-proto/workflowservicemock"
	"google.golang.org/grpc/codes"
)

func TestErrorWrapper_SimpleError(t *testing.T) {
	require := require.New(t)
	wrapper := NewWorkflowServiceErrorWrapper(workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t)))

	st := status.Error(codes.NotFound, "Something not found")

	svcerr := wrapper.(*workflowServiceErrorWrapper).convertError(st)
	require.IsType(&serviceerror.NotFound{}, svcerr)
	require.Equal("Something not found", svcerr.Error())
}

func TestErrorWrapper_ErrorWithFailure(t *testing.T) {
	require := require.New(t)
	wrapper := NewWorkflowServiceErrorWrapper(workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t)))

	st, _ := status.New(codes.AlreadyExists, "Something started").WithDetails(&failure.WorkflowExecutionAlreadyStarted{
		StartRequestId: "srId",
		RunId:          "rId",
	})

	svcerr := wrapper.(*workflowServiceErrorWrapper).convertError(st.Err())
	require.IsType(&serviceerror.WorkflowExecutionAlreadyStarted{}, svcerr)
	require.Equal("Something started", svcerr.Error())
	weasErr := svcerr.(*serviceerror.WorkflowExecutionAlreadyStarted)
	require.Equal("rId", weasErr.RunId)
	require.Equal("srId", weasErr.StartRequestId)
}
