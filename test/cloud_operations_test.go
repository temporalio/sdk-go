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

package test_test

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/cloud/cloudservice/v1"
	"go.temporal.io/sdk/client"
)

var cloudOperationsTestsFlag bool

func init() {
	flag.BoolVar(&cloudOperationsTestsFlag, "cloud-operations-tests", false, "Enable cloud operations tests")
}

func TestCloudOperationsSuite(t *testing.T) {
	// Skip if cloud tests not enabled
	if !cloudOperationsTestsFlag {
		t.Skip("Cloud operations tests disabled")
	}
	suite.Run(t, new(CloudOperationsTestSuite))
}

type CloudOperationsTestSuite struct {
	*require.Assertions
	suite.Suite

	client client.CloudOperationsClient

	namespace  string
	apiKey     string
	apiVersion string
}

func (c *CloudOperationsTestSuite) SetupSuite() {
	c.Assertions = require.New(c.T())
	c.namespace = os.Getenv("TEMPORAL_NAMESPACE")
	c.NotEmpty(c.namespace)
	c.apiKey = os.Getenv("TEMPORAL_CLIENT_CLOUD_API_KEY")
	c.NotEmpty(c.apiKey)
	c.apiVersion = os.Getenv("TEMPORAL_CLIENT_CLOUD_API_VERSION")
	c.NotEmpty(c.apiVersion)
}

func (c *CloudOperationsTestSuite) TearDownSuite() {
}

func (c *CloudOperationsTestSuite) SetupTest() {
	var err error
	c.client, err = client.DialCloudOperationsClient(context.Background(), client.CloudOperationsClientOptions{
		Version:     c.apiVersion,
		Credentials: client.NewAPIKeyStaticCredentials(c.apiKey),
	})
	c.NoError(err)
}

func (c *CloudOperationsTestSuite) TearDownTest() {
	if c.client != nil {
		c.client.Close()
	}
}

func (c *CloudOperationsTestSuite) TestSimpleGetNamespace() {
	resp, err := c.client.CloudService().GetNamespace(
		context.Background(),
		&cloudservice.GetNamespaceRequest{Namespace: c.namespace},
	)
	c.NoError(err)
	c.Equal(c.namespace, resp.Namespace.Namespace)
}
