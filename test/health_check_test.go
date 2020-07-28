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
