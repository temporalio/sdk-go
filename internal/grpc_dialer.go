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
	"time"

	"github.com/gogo/status"
	"github.com/uber-go/tally"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"

	"go.temporal.io/sdk/internal/common/metrics"
)

type (
	// dialParameters are passed to GRPCDialer and must be used to create gRPC connection.
	dialParameters struct {
		HostPort              string
		UserConnectionOptions ConnectionOptions
		RequiredInterceptors  []grpc.UnaryClientInterceptor
		DefaultServiceConfig  string
	}
)

const (
	// LocalHostPort is a default host:port for worker and client to connect to.
	LocalHostPort = "localhost:7233"

	// defaultServiceConfig is a default gRPC connection service config which enables DNS round-robin between IPs.
	defaultServiceConfig = `{"loadBalancingConfig": [{"round_robin":{}}]}`

	// minConnectTimeout is the minimum amount of time we are willing to give a connection to complete.
	minConnectTimeout = 20 * time.Second
)

func dial(params dialParameters) (*grpc.ClientConn, error) {
	grpcSecurityOptions := grpc.WithInsecure()
	if params.UserConnectionOptions.TLS != nil {
		grpcSecurityOptions = grpc.WithTransportCredentials(credentials.NewTLS(params.UserConnectionOptions.TLS))
	}

	// gRPC maintains connection pool inside grpc.ClientConn.
	// This connection pool has auto reconnect feature.
	// If connection goes down, gRPC will try to reconnect using exponential backoff strategy:
	// https://github.com/grpc/grpc/blob/master/doc/connection-backoff.md.
	// Default MaxDelay is 120 seconds which is too high.
	// Setting it to retryPollOperationMaxInterval here will correlate with poll reconnect interval.
	var cp = grpc.ConnectParams{
		Backoff:           backoff.DefaultConfig,
		MinConnectTimeout: minConnectTimeout,
	}
	cp.Backoff.BaseDelay = retryPollOperationInitialInterval
	cp.Backoff.MaxDelay = retryPollOperationMaxInterval

	return grpc.Dial(params.HostPort,
		grpcSecurityOptions,
		grpc.WithChainUnaryInterceptor(params.RequiredInterceptors...),
		grpc.WithDefaultServiceConfig(params.DefaultServiceConfig),
		grpc.WithConnectParams(cp),
	)
}

func requiredInterceptors(metricScope tally.Scope) []grpc.UnaryClientInterceptor {
	return []grpc.UnaryClientInterceptor{metrics.NewScopeInterceptor(metricScope), errorInterceptor}
}

func errorInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	err = serviceerror.FromStatus(status.Convert(err))
	return err
}
