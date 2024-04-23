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
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/cloud/cloudservice/v1"
	"go.temporal.io/sdk/client"
)

var cloudTestsFlag bool

func init() { flag.BoolVar(&cloudTestsFlag, "cloud-tests", false, "Enable cloud tests") }

func TestCloudSuite(t *testing.T) {
	// Skip if cloud tests not enabled
	if !cloudTestsFlag {
		t.Skip("Cloud tests disabled")
	}
	suite.Run(t, new(CloudTestSuite))
}

type CloudTestSuite struct {
	*require.Assertions
	suite.Suite

	config Config
	client client.CloudOperationsClient
}

func (c *CloudTestSuite) SetupSuite() {
	c.Assertions = require.New(c.T())
}

func (c *CloudTestSuite) TearDownSuite() {
}

func (c *CloudTestSuite) SetupTest() {
	var err error
	c.client, err = client.DialCloudOperationsClient(context.Background(), client.CloudOperationsClientOptions{
		ConnectionOptions: client.ConnectionOptions{TLS: c.config.TLS},
	})
	c.NoError(err)
}

func (c *CloudTestSuite) TearDownTest() {
	if c.client != nil {
		c.client.Close()
	}
}

func (c *CloudTestSuite) TestSimpleGetNamespace() {
	resp, err := c.client.CloudService().GetNamespace(
		context.Background(),
		&cloudservice.GetNamespaceRequest{Namespace: c.config.Namespace},
	)
	c.NoError(err)
	c.Equal(c.config.Namespace, resp.Namespace.Namespace)
}
