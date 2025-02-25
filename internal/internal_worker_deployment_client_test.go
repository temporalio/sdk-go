// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/deployment/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
)

// worker deployment client test suite
type (
	workerDeploymentClientTestSuite struct {
		suite.Suite
		mockCtrl      *gomock.Controller
		service       *workflowservicemock.MockWorkflowServiceClient
		client        Client
		dataConverter converter.DataConverter
	}
)

func TestWorkerDeploymentClientSuite(t *testing.T) {
	suite.Run(t, new(workerDeploymentClientTestSuite))
}

func (d *workerDeploymentClientTestSuite) SetupTest() {
	d.mockCtrl = gomock.NewController(d.T())
	d.service = workflowservicemock.NewMockWorkflowServiceClient(d.mockCtrl)
	d.service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	d.client = NewServiceClient(d.service, nil, ClientOptions{})
	d.dataConverter = converter.GetDefaultDataConverter()
}

func (d *workerDeploymentClientTestSuite) TearDownTest() {
	d.mockCtrl.Finish() // assert mock’s expectations
}

func getListWorkerDeploymentsRequest() *workflowservice.ListWorkerDeploymentsRequest {
	request := &workflowservice.ListWorkerDeploymentsRequest{
		Namespace: DefaultNamespace,
	}

	return request
}

// WorkerDeploymentIterator

func (d *workerDeploymentClientTestSuite) TestWorkerDeploymentIterator_NoError() {
	request1 := getListWorkerDeploymentsRequest()
	response1 := &workflowservice.ListWorkerDeploymentsResponse{
		WorkerDeployments: []*workflowservice.ListWorkerDeploymentsResponse_WorkerDeploymentSummary{
			{
				Name: "foo1",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getListWorkerDeploymentsRequest()
	request2.NextPageToken = response1.NextPageToken
	response2 := &workflowservice.ListWorkerDeploymentsResponse{
		WorkerDeployments: []*workflowservice.ListWorkerDeploymentsResponse_WorkerDeploymentSummary{
			{
				Name: "foo2",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	request3 := getListWorkerDeploymentsRequest()
	request3.NextPageToken = response2.NextPageToken
	response3 := &workflowservice.ListWorkerDeploymentsResponse{
		WorkerDeployments: []*workflowservice.ListWorkerDeploymentsResponse_WorkerDeploymentSummary{
			{
				Name: "foo3",
			},
		},
		NextPageToken: nil,
	}

	d.service.EXPECT().ListWorkerDeployments(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)
	d.service.EXPECT().ListWorkerDeployments(gomock.Any(), request2, gomock.Any()).Return(response2, nil).Times(1)
	d.service.EXPECT().ListWorkerDeployments(gomock.Any(), request3, gomock.Any()).Return(response3, nil).Times(1)

	var events []*WorkerDeploymentListEntry
	iter, _ := d.client.WorkerDeploymentClient().List(context.Background(), WorkerDeploymentListOptions{})
	for iter.HasNext() {
		event, err := iter.Next()
		d.Nil(err)
		events = append(events, event)
	}
	d.Equal(3, len(events))
}

func (d *workerDeploymentClientTestSuite) TestWorkerDeploymentIteratorError() {
	request1 := getListWorkerDeploymentsRequest()
	response1 := &workflowservice.ListWorkerDeploymentsResponse{
		WorkerDeployments: []*workflowservice.ListWorkerDeploymentsResponse_WorkerDeploymentSummary{
			{
				Name: "foo1",
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	request2 := getListWorkerDeploymentsRequest()
	request2.NextPageToken = response1.NextPageToken

	d.service.EXPECT().ListWorkerDeployments(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)

	iter, _ := d.client.WorkerDeploymentClient().List(context.Background(), WorkerDeploymentListOptions{})

	d.True(iter.HasNext())
	event, err := iter.Next()
	d.NotNil(event)
	d.Nil(err)

	d.service.EXPECT().ListWorkerDeployments(gomock.Any(), request2, gomock.Any()).Return(nil, serviceerror.NewNotFound("")).Times(1)

	d.True(iter.HasNext())
	event, err = iter.Next()
	d.Nil(event)
	d.NotNil(err)
}

// nil timestamps pass IsZero()
func (d *workerDeploymentClientTestSuite) TestWorkerDeploymenNilTimestamp() {
	request := &workflowservice.DescribeWorkerDeploymentRequest{
		Namespace:      DefaultNamespace,
		DeploymentName: "foo",
	}

	response := &workflowservice.DescribeWorkerDeploymentResponse{
		ConflictToken: []byte{1, 2, 1, 2, 1, 1, 8},
		WorkerDeploymentInfo: &deployment.WorkerDeploymentInfo{
			Name:       "foo",
			CreateTime: nil,
		},
	}

	d.service.EXPECT().DescribeWorkerDeployment(gomock.Any(), request, gomock.Any()).Return(response, nil).Times(1)

	dHandle := d.client.WorkerDeploymentClient().GetHandle("foo")
	deployment, _ := dHandle.Describe(context.Background(), WorkerDeploymentDescribeOptions{})
	d.True(deployment.Info.CreateTime.IsZero())
}
