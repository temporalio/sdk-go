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
	deploymentpb "go.temporal.io/api/deployment/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/converter"
)

// deployment client test suite
type (
	deploymentClientTestSuite struct {
		suite.Suite
		mockCtrl      *gomock.Controller
		service       *workflowservicemock.MockWorkflowServiceClient
		client        Client
		dataConverter converter.DataConverter
	}
)

func TestDeploymentClientSuite(t *testing.T) {
	suite.Run(t, new(deploymentClientTestSuite))
}

func (d *deploymentClientTestSuite) SetupTest() {
	d.mockCtrl = gomock.NewController(d.T())
	d.service = workflowservicemock.NewMockWorkflowServiceClient(d.mockCtrl)
	d.service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	d.client = NewServiceClient(d.service, nil, ClientOptions{})
	d.dataConverter = converter.GetDefaultDataConverter()
}

func (d *deploymentClientTestSuite) TearDownTest() {
	d.mockCtrl.Finish() // assert mock’s expectations
}

func (d *deploymentClientTestSuite) TestSetCurrentDeployment() {
	metadata := map[string]interface{}{
		"data1": "metadata 1",
	}

	options := DeploymentSetCurrentOptions{
		Deployment: Deployment{
			BuildID:    "bid1",
			SeriesName: "series1",
		},
		ClientIdentity: "me",
		MetadataUpdate: DeploymentMetadataUpdate{
			UpsertEntries: metadata,
			RemoveEntries: []string{"never"},
		},
	}
	createResp := &workflowservice.SetCurrentDeploymentResponse{}

	d.service.EXPECT().SetCurrentDeployment(gomock.Any(), gomock.Any(), gomock.Any()).Return(createResp, nil).
		Do(func(_ interface{}, req *workflowservice.SetCurrentDeploymentRequest, _ ...interface{}) {
			var resultMeta string
			// verify the metadata
			err := converter.GetDefaultDataConverter().FromPayload(req.UpdateMetadata.UpsertEntries["data1"], &resultMeta)
			d.NoError(err)
			d.Equal("metadata 1", resultMeta)

			d.Equal(req.UpdateMetadata.RemoveEntries, []string{"never"})
			d.Equal(req.Identity, "me")
			d.Equal(req.Deployment.BuildId, "bid1")
			d.Equal(req.Deployment.SeriesName, "series1")
		})
	_, _ = d.client.DeploymentClient().SetCurrent(context.Background(), options)
}

func getListDeploymentsRequest() *workflowservice.ListDeploymentsRequest {
	request := &workflowservice.ListDeploymentsRequest{
		Namespace: DefaultNamespace,
	}

	return request
}

// DeploymentIterator

func (d *deploymentClientTestSuite) TestDeploymentIterator_NoError() {
	request1 := getListDeploymentsRequest()
	response1 := &workflowservice.ListDeploymentsResponse{
		Deployments: []*deploymentpb.DeploymentListInfo{
			{
				IsCurrent: false,
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}
	request2 := getListDeploymentsRequest()
	request2.NextPageToken = response1.NextPageToken
	response2 := &workflowservice.ListDeploymentsResponse{
		Deployments: []*deploymentpb.DeploymentListInfo{
			{
				IsCurrent: false,
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	request3 := getListDeploymentsRequest()
	request3.NextPageToken = response2.NextPageToken
	response3 := &workflowservice.ListDeploymentsResponse{
		Deployments: []*deploymentpb.DeploymentListInfo{
			{
				IsCurrent: false,
			},
		},
		NextPageToken: nil,
	}

	d.service.EXPECT().ListDeployments(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)
	d.service.EXPECT().ListDeployments(gomock.Any(), request2, gomock.Any()).Return(response2, nil).Times(1)
	d.service.EXPECT().ListDeployments(gomock.Any(), request3, gomock.Any()).Return(response3, nil).Times(1)

	var events []*DeploymentListEntry
	iter, _ := d.client.DeploymentClient().List(context.Background(), DeploymentListOptions{})
	for iter.HasNext() {
		event, err := iter.Next()
		d.Nil(err)
		events = append(events, event)
	}
	d.Equal(3, len(events))
}

func (d *deploymentClientTestSuite) TestIteratorError() {
	request1 := getListDeploymentsRequest()
	response1 := &workflowservice.ListDeploymentsResponse{
		Deployments: []*deploymentpb.DeploymentListInfo{
			{
				IsCurrent: false,
			},
		},
		NextPageToken: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
	}

	request2 := getListDeploymentsRequest()
	request2.NextPageToken = response1.NextPageToken

	d.service.EXPECT().ListDeployments(gomock.Any(), request1, gomock.Any()).Return(response1, nil).Times(1)

	iter, _ := d.client.DeploymentClient().List(context.Background(), DeploymentListOptions{})

	d.True(iter.HasNext())
	event, err := iter.Next()
	d.NotNil(event)
	d.Nil(err)

	d.service.EXPECT().ListDeployments(gomock.Any(), request2, gomock.Any()).Return(nil, serviceerror.NewNotFound("")).Times(1)

	d.True(iter.HasNext())
	event, err = iter.Next()
	d.Nil(event)
	d.NotNil(err)
}
