package test_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/log"
)

type HealthCheckTestSuite struct {
	suite.Suite
	client client.Client
	config Config
}

func TestHealthCheckTestSuite(t *testing.T) {
	suite.Run(t, &HealthCheckTestSuite{})
}

func (ts *HealthCheckTestSuite) SetupSuite() {
	ts.config = NewConfig()
	var err error
	ts.client, err = client.NewClient(client.Options{
		HostPort:  ts.config.ServiceAddr,
		Namespace: namespace,
		Logger:    log.NewDefaultLogger(),
	})
	ts.Require().NoError(err)
}

func (ts *HealthCheckTestSuite) TearDownSuite() {
	ts.client.Close()
}

func (ts *HealthCheckTestSuite) TestHealthCheckSuccess() {
	code, err := ts.client.CheckHealth(context.Background())
	ts.Require().NoError(err, "Is service running at %v ?", ts.config.ServiceAddr)
	ts.Equal(healthpb.HealthCheckResponse_SERVING, code)
}
