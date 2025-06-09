package metrics_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestGRPCInterceptor(t *testing.T) {
	// Start a health gRPC server
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("myservice", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(srv, healthServer)
	defer srv.Stop()
	go func() { _ = srv.Serve(l) }()
	time.Sleep(100 * time.Millisecond)

	// Create client with interceptor
	handler := metrics.NewCapturingHandler()
	cc, err := grpc.NewClient(l.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(metrics.NewGRPCInterceptor(handler, "_my_suffix", true)))
	require.NoError(t, err)
	defer func() { _ = cc.Close() }()
	client := grpc_health_v1.NewHealthClient(cc)

	// Make successful call
	_, err = client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: "myservice"})
	require.NoError(t, err)

	// Check counters and timers
	counters := handler.Counters()
	require.Len(t, counters, 1)
	require.Equal(t, metrics.TemporalRequest+"_my_suffix", counters[0].Name)
	require.Equal(t, map[string]string{metrics.OperationTagName: "Check", metrics.NamespaceTagName: "_unknown_"}, counters[0].Tags)
	require.Equal(t, int64(1), counters[0].Value())
	timers := handler.Timers()
	require.Len(t, timers, 1)
	require.Equal(t, metrics.TemporalRequestLatency+"_my_suffix", timers[0].Name)
	require.Equal(t, map[string]string{metrics.OperationTagName: "Check", metrics.NamespaceTagName: "_unknown_"}, timers[0].Tags)

	// Now clear the metrics and set a handler with tags and long poll on the
	// context and make a known failing call
	handler.Clear()
	ctx := context.WithValue(context.Background(), metrics.HandlerContextKey{},
		handler.WithTags(map[string]string{"roottag": "roottagval"}))
	ctx = context.WithValue(ctx, metrics.LongPollContextKey{}, true)
	_, err = client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "unknown"})
	require.Error(t, err)

	// Check counters
	counters = handler.Counters()
	require.Len(t, counters, 2)
	require.Equal(t, metrics.TemporalLongRequest+"_my_suffix", counters[0].Name)
	require.Equal(t, map[string]string{metrics.OperationTagName: "Check", "roottag": "roottagval", metrics.NamespaceTagName: "_unknown_"}, counters[0].Tags)
	require.Equal(t, int64(1), counters[0].Value())
	require.Equal(t, metrics.TemporalLongRequestFailure+"_my_suffix", counters[1].Name)
	require.Equal(t, map[string]string{metrics.OperationTagName: "Check", "roottag": "roottagval", metrics.NamespaceTagName: "_unknown_"}, counters[1].Tags)
	require.Equal(t, int64(1), counters[1].Value())
}
