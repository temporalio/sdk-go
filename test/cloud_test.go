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
