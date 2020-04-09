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

const (
	// LocalHostPort is a default host:port for worker and client to connect to.
	LocalHostPort = "localhost:7233"

	// defaultServiceConfig is a default gRPC connection service config which enables DNS round-robin between IPs.
	defaultServiceConfig = `{"loadBalancingConfig": [{"round_robin":{}}]}`
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
