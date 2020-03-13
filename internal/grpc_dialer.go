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

	"github.com/gogo/status"
	"github.com/uber-go/tally"
	"go.temporal.io/temporal-proto/serviceerror"
	"google.golang.org/grpc"

	"go.temporal.io/temporal/internal/common/metrics"
)

type (
	// GRPCDialerParams are passed to GRPCDialer and must be used to create gRPC connection.
	GRPCDialerParams struct {
		HostPort             string
		RequiredInterceptors []grpc.UnaryClientInterceptor
		DefaultServiceConfig string
	}

	// GRPCDialer creates gRPC connection.
	GRPCDialer func(params GRPCDialerParams) (*grpc.ClientConn, error)
)

func defaultGRPCDialer(params GRPCDialerParams) (*grpc.ClientConn, error) {
	return grpc.Dial(params.HostPort,
		grpc.WithInsecure(),
		grpc.WithChainUnaryInterceptor(params.RequiredInterceptors...),
		grpc.WithDefaultServiceConfig(params.DefaultServiceConfig),
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
