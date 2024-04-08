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
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/errordetails/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/retry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestErrorWrapper_SimpleError(t *testing.T) {
	require := require.New(t)

	svcerr := errorInterceptor(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return status.Error(codes.NotFound, "Something not found")
		})

	require.IsType(&serviceerror.NotFound{}, svcerr)
	require.Equal("Something not found", svcerr.Error())
}

func TestErrorWrapper_ErrorWithFailure(t *testing.T) {
	require := require.New(t)

	svcerr := errorInterceptor(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			st, _ := status.New(codes.AlreadyExists, "Something started").WithDetails(&errordetails.WorkflowExecutionAlreadyStartedFailure{
				StartRequestId: "srId",
				RunId:          "rId",
			})

			return st.Err()
		})

	require.IsType(&serviceerror.WorkflowExecutionAlreadyStarted{}, svcerr)
	require.Equal("Something started", svcerr.Error())
	weasErr := svcerr.(*serviceerror.WorkflowExecutionAlreadyStarted)
	require.Equal("rId", weasErr.RunId)
	require.Equal("srId", weasErr.StartRequestId)
}

type authHeadersProvider struct {
	token string
	err   error
}

func (a authHeadersProvider) GetHeaders(context.Context) (map[string]string, error) {
	if a.err != nil {
		return nil, a.err
	}
	headers := make(map[string]string)
	headers["authorization"] = a.token
	return headers, nil
}

func TestHeadersProvider_PopulateAuthToken(t *testing.T) {
	require.NoError(t, headersProviderInterceptor(authHeadersProvider{token: "test-auth-token"})(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				return errors.New("unable to get outgoing context metadata")
			}
			require.Equal(t, 1, len(md.Get("authorization")))
			if md.Get("authorization")[0] != "test-auth-token" {
				return errors.New("auth token hasn't been set")
			}
			return nil
		}))
}

func TestHeadersProvider_Error(t *testing.T) {
	require.Error(t, headersProviderInterceptor(authHeadersProvider{err: errors.New("failed to populate headers")})(context.Background(), "method", "request", "reply", nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			return nil
		}))
}

func TestHeadersProvider_NotIncludedWhenNil(t *testing.T) {
	interceptors := requiredInterceptors(nil, nil, nil, nil, nil)
	require.Equal(t, 5, len(interceptors))
}

func TestHeadersProvider_IncludedWithHeadersProvider(t *testing.T) {
	interceptors := requiredInterceptors(nil,
		authHeadersProvider{token: "test-auth-token"}, nil, nil, nil)
	require.Equal(t, 6, len(interceptors))
}

func TestMissingGetServerInfo(t *testing.T) {
	// Make a gRPC server that has everything unimplemented
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	go func() {
		if err := srv.Serve(l); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait until it is responding with a 404
	var lastErr error
	for i := 0; i < 20; i++ {
		lastErr = nil
		//lint:ignore SA1019 test
		conn, err := grpc.Dial(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
		} else {
			_, err := workflowservice.NewWorkflowServiceClient(conn).GetSystemInfo(
				context.Background(),
				&workflowservice.GetSystemInfoRequest{},
			)
			_ = conn.Close()
			if status.Code(err) != codes.Unimplemented {
				lastErr = fmt.Errorf("expected unimplemented, got: %w", err)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, lastErr)

	// Create a new client and confirm client has empty capabilities set
	client, err := DialClient(context.Background(), ClientOptions{HostPort: l.Addr().String()})
	require.NoError(t, err)
	workflowClient := client.(*WorkflowClient)
	require.True(t, proto.Equal(&workflowservice.GetSystemInfoResponse_Capabilities{}, workflowClient.capabilities))
}

func TestInternalErrorRetry(t *testing.T) {
	// Build a common retry policy that will retry 2 times (so 3 attempts total)
	retryConfig := retry.NewGrpcRetryConfig(10 * time.Nanosecond)
	retryConfig.SetMaximumAttempts(3)
	ctx := context.WithValue(context.Background(), retry.ConfigKey, retryConfig)

	// Start a server that wants you to retry internal errors (the default)
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()

	// Set it to return an internal error on signal workflow
	srv.signalWorkflowExecutionResponseError = status.Error(codes.Internal, "oh no, an internal error")

	// Create client and make call
	client, err := DialClient(context.Background(), ClientOptions{HostPort: srv.addr})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.WorkflowService().SignalWorkflowExecution(ctx, &workflowservice.SignalWorkflowExecutionRequest{})
	// Confirm it made 3 calls
	_, isInternalError := err.(*serviceerror.Internal)
	require.True(t, isInternalError)
	require.Equal(t, 3, srv.signalWorkflowInvokeCount())

	// Now make a server that does not retry internal errors
	srv, err = startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()
	srv.getSystemInfoResponse.Capabilities = &workflowservice.GetSystemInfoResponse_Capabilities{
		InternalErrorDifferentiation: true,
	}

	// Set it to return an internal error on signal workflow
	srv.signalWorkflowExecutionResponseError = status.Error(codes.Internal, "oh no, an internal error")

	// Create client and make call
	client, err = DialClient(context.Background(), ClientOptions{HostPort: srv.addr})
	require.NoError(t, err)
	defer client.Close()
	_, err = client.WorkflowService().SignalWorkflowExecution(ctx, &workflowservice.SignalWorkflowExecutionRequest{})
	// Confirm it only made 1 call because it doesn't retry internal
	_, isInternalError = err.(*serviceerror.Internal)
	require.True(t, isInternalError)
	require.Equal(t, 1, srv.signalWorkflowInvokeCount())
}

func TestEagerAndLazyClient(t *testing.T) {
	// Start a server that always returns an error on get system info
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()
	srv.getSystemInfoResponseError = fmt.Errorf("some server failure")

	// Confirm eager dial fails
	_, err = DialClient(context.Background(), ClientOptions{HostPort: srv.addr})
	require.EqualError(t, err, "failed reaching server: some server failure")

	// Confirm lazy dial succeeds but fails signal workflow
	c, err := NewLazyClient(ClientOptions{HostPort: srv.addr})
	require.NoError(t, err)
	defer c.Close()
	err = c.SignalWorkflow(context.Background(), "workflow1", "", "my-signal", nil)
	require.EqualError(t, err, "failed reaching server: some server failure")

	// But if we call again without a sys info response error, it will succeed
	srv.getSystemInfoResponseError = nil
	err = c.SignalWorkflow(context.Background(), "workflow1", "", "my-signal", nil)
	require.NoError(t, err)

	// Now that there's no sys info response error, eager should succeed
	c, err = DialClient(context.Background(), ClientOptions{HostPort: srv.addr})
	require.NoError(t, err)
	defer c.Close()

	// And even if it starts erroring, the success was memoized so calls succeed
	srv.getSystemInfoResponseError = fmt.Errorf("some server failure")
	err = c.SignalWorkflow(context.Background(), "workflow1", "", "my-signal", nil)
	require.NoError(t, err)
}

func TestCheckHealth(t *testing.T) {
	// Start a gRPC server and lazy client
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()
	c, err := NewLazyClient(ClientOptions{HostPort: srv.addr})
	require.NoError(t, err)
	defer c.Close()

	// Confirm fail if can't init
	srv.getSystemInfoResponseError = fmt.Errorf("some server failure")
	_, err = c.CheckHealth(context.Background(), nil)
	require.EqualError(t, err, "failed reaching server: some server failure")

	// Now if it can init, but health not registered
	srv.getSystemInfoResponseError = nil
	_, err = c.CheckHealth(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "health check error")

	// Now register the service but set it as bad
	srv.healthServer.SetServingStatus("temporal.api.workflowservice.v1.WorkflowService",
		grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	_, err = c.CheckHealth(context.Background(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "NOT_SERVING")

	// Now set as serving and succeed
	srv.healthServer.SetServingStatus("temporal.api.workflowservice.v1.WorkflowService",
		grpc_health_v1.HealthCheckResponse_SERVING)
	_, err = c.CheckHealth(context.Background(), nil)
	require.NoError(t, err)
}

func TestDialOptions(t *testing.T) {
	// Start an unimplemented gRPC server
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()

	// Connect with unary outer and unary inner interceptors
	var trace []string
	tracer := func(name string) grpc.UnaryClientInterceptor {
		return func(
			ctx context.Context,
			method string,
			req interface{},
			reply interface{},
			cc *grpc.ClientConn,
			invoker grpc.UnaryInvoker,
			opts ...grpc.CallOption,
		) error {
			if strings.HasSuffix(method, "/SignalWorkflowExecution") {
				trace = append(trace, "begin "+name)
				defer func() { trace = append(trace, "end "+name) }()
			}
			return invoker(ctx, method, req, reply, cc, opts...)
		}
	}
	client, err := DialClient(context.Background(), ClientOptions{
		HostPort: srv.addr,
		ConnectionOptions: ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithUnaryInterceptor(tracer("outer")),
				grpc.WithChainUnaryInterceptor(tracer("inner1"), tracer("inner2")),
			},
		},
	})
	require.NoError(t, err)
	defer client.Close()

	// Make call we know will error (ignore error)
	_, _ = client.WorkflowService().SignalWorkflowExecution(context.TODO(),
		&workflowservice.SignalWorkflowExecutionRequest{})

	// Confirm trace
	expected := []string{"begin outer", "begin inner1", "begin inner2", "end inner2", "end inner1", "end outer"}
	require.Equal(t, expected, trace)
}

func TestCustomResolver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Create two gRPC servers
	s1, err := startTestGRPCServer()
	require.NoError(t, err)
	defer s1.Stop()
	s2, err := startTestGRPCServer()
	require.NoError(t, err)
	defer s2.Stop()

	// Register resolver for both IPs and create client using it
	scheme := "test-resolve-" + uuid.New()
	builder := manual.NewBuilderWithScheme(scheme)
	builder.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: s1.addr}, {Addr: s2.addr}}})
	resolver.Register(builder)
	client, err := DialClient(context.Background(), ClientOptions{HostPort: scheme + ":///whatever"})
	require.NoError(t, err)
	defer client.Close()

	// Round-robin appears to apply to transport _connections_ rather than just
	// addresses. As such we spin here until we have round-tripped an RPC to
	// both servers to guarantee that connections to both have been established.
	// This test can fail spuriously without this section as the calls to
	// SignalWorkflow below will race with grpc-go's connection establishment.
	// This technique is consistent with the approach used in the grpc-go
	// codebase itself:
	// https://github.com/grpc/grpc-go/blob/bd7076973b45b81e37a45eb761efb789e2001618/balancer/roundrobin/roundrobin_test.go#L196-L212
	connected := map[net.Addr]struct{}{}
	req := workflowservice.SignalWorkflowExecutionRequest{
		WorkflowExecution: &common.WorkflowExecution{WorkflowId: "workflowid", RunId: "runid"},
		SignalName:        "signal",
		Namespace:         DefaultNamespace,
		Identity:          t.Name(),
	}
	var peerOut peer.Peer
	for len(connected) < 2 {
		req.RequestId = uuid.New()
		_, err := client.WorkflowService().SignalWorkflowExecution(context.Background(), &req, grpc.Peer(&peerOut))
		if err == nil {
			connected[peerOut.Addr] = struct{}{}
		}
	}

	// reset invocation counts to initial state
	s1.resetSignalWorkflowInvokeCount()
	s2.resetSignalWorkflowInvokeCount()

	// Confirm round robin'd
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.Equal(t, 1, s1.signalWorkflowInvokeCount())
	require.Equal(t, 1, s2.signalWorkflowInvokeCount())
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.Equal(t, 2, s1.signalWorkflowInvokeCount())
	require.Equal(t, 2, s2.signalWorkflowInvokeCount())

	// Now shutdown the first one and confirm second now receives requests
	s1.Stop()
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.Equal(t, 2, s1.signalWorkflowInvokeCount())
	require.Equal(t, 3, s2.signalWorkflowInvokeCount())
	require.NoError(t, client.SignalWorkflow(ctx, "workflowid", "runid", "signalname", nil))
	require.Equal(t, 2, s1.signalWorkflowInvokeCount())
	require.Equal(t, 4, s2.signalWorkflowInvokeCount())
}

func TestResourceExhaustedCause(t *testing.T) {
	// Start gRPC server
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()
	handler := metrics.NewCapturingHandler()

	// Attempt dial with a resource exhausted cause
	s, _ := status.New(codes.ResourceExhausted, "some resource exhausted").WithDetails(&errordetails.ResourceExhaustedFailure{
		Cause: enums.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT,
	})
	srv.getSystemInfoResponseError = s.Err()
	_, err = DialClient(context.Background(), ClientOptions{HostPort: srv.addr, MetricsHandler: handler})
	require.Error(t, err)

	// Attempt dial with a cause-less resource exhausted
	srv.getSystemInfoResponseError = status.New(codes.ResourceExhausted, "some resource exhausted").Err()
	_, err = DialClient(context.Background(), ClientOptions{HostPort: srv.addr, MetricsHandler: handler})
	require.Error(t, err)

	// Make sure we have 1 metric with cause and 1 without
	var foundWithCause, foundWithoutCause bool
	for _, counter := range handler.Counters() {
		t.Logf("%+v", counter)
		if counter.Tags["operation"] == "GetSystemInfo" && counter.Tags["cause"] == "ConcurrentLimit" {
			foundWithCause = true
		} else if counter.Tags["operation"] == "GetSystemInfo" && counter.Tags["cause"] == "Unspecified" {
			foundWithoutCause = true
		}
	}
	assert.True(t, foundWithCause)
	assert.True(t, foundWithoutCause)
}

func TestCredentialsAPIKey(t *testing.T) {
	srv, err := startTestGRPCServer()
	require.NoError(t, err)
	defer srv.Stop()

	// Fixed string
	client, err := DialClient(context.Background(), ClientOptions{
		HostPort:    srv.addr,
		Credentials: NewAPIKeyStaticCredentials("my-api-key"),
	})
	require.NoError(t, err)
	defer client.Close()
	require.Equal(
		t,
		[]string{"Bearer my-api-key"},
		metadata.ValueFromIncomingContext(srv.getSystemInfoRequestContext, "Authorization"),
	)

	// Overwrite via context
	_, err = client.WorkflowService().GetSystemInfo(
		metadata.AppendToOutgoingContext(context.Background(), "authorization", "overridden value"),
		&workflowservice.GetSystemInfoRequest{},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		[]string{"overridden value"},
		metadata.ValueFromIncomingContext(srv.getSystemInfoRequestContext, "Authorization"),
	)

	// Callback
	client, err = DialClient(context.Background(), ClientOptions{
		HostPort: srv.addr,
		Credentials: NewAPIKeyDynamicCredentials(func(ctx context.Context) (string, error) {
			return "my-callback-api-key", nil
		}),
	})
	require.NoError(t, err)
	defer client.Close()
	require.Equal(
		t,
		[]string{"Bearer my-callback-api-key"},
		metadata.ValueFromIncomingContext(srv.getSystemInfoRequestContext, "Authorization"),
	)
}

func TestCredentialsMTLS(t *testing.T) {
	// Just confirming option is set, not full end-to-end mTLS test

	// No TLS set
	var clientOptions ClientOptions
	creds := NewMTLSCredentials(tls.Certificate{Certificate: [][]byte{[]byte("somedata1")}})
	require.NoError(t, creds.applyToOptions(&clientOptions))
	require.Equal(t, "somedata1", string(clientOptions.ConnectionOptions.TLS.Certificates[0].Certificate[0]))

	// TLS already set
	clientOptions = ClientOptions{}
	clientOptions.ConnectionOptions.TLS = &tls.Config{ServerName: "my-server-name"}
	creds = NewMTLSCredentials(tls.Certificate{Certificate: [][]byte{[]byte("somedata2")}})
	require.NoError(t, creds.applyToOptions(&clientOptions))
	require.Equal(t, "my-server-name", clientOptions.ConnectionOptions.TLS.ServerName)
	require.Equal(t, "somedata2", string(clientOptions.ConnectionOptions.TLS.Certificates[0].Certificate[0]))

	// Fail with existing cert
	clientOptions = ClientOptions{}
	clientOptions.ConnectionOptions.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{[]byte("somedata3")}}},
	}
	creds = NewMTLSCredentials(tls.Certificate{Certificate: [][]byte{[]byte("somedata4")}})
	require.Error(t, creds.applyToOptions(&clientOptions))
}

type testGRPCServer struct {
	workflowservice.UnimplementedWorkflowServiceServer
	*grpc.Server
	addr                                 string
	healthServer                         *health.Server
	sigWfCount                           int32
	getSystemInfoRequestContext          context.Context
	getSystemInfoResponse                workflowservice.GetSystemInfoResponse
	getSystemInfoResponseError           error
	signalWorkflowExecutionResponse      workflowservice.SignalWorkflowExecutionResponse
	signalWorkflowExecutionResponseError error
}

func startTestGRPCServer() (*testGRPCServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	t := &testGRPCServer{
		Server:       grpc.NewServer(),
		addr:         l.Addr().String(),
		healthServer: health.NewServer(),
	}
	workflowservice.RegisterWorkflowServiceServer(t.Server, t)
	grpc_health_v1.RegisterHealthServer(t.Server, t.healthServer)
	go func() {
		if err := t.Serve(l); err != nil {
			log.Fatal(err)
		}
	}()

	// Wait until get-system-info reports serving
	return t, t.waitUntilServing()
}

func (t *testGRPCServer) waitUntilServing() error {
	// Try 20 times, waiting 100ms between
	var lastErr error
	for i := 0; i < 20; i++ {
		//lint:ignore SA1019 test
		conn, err := grpc.Dial(t.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
		} else {
			_, err := workflowservice.NewWorkflowServiceClient(conn).GetSystemInfo(
				context.Background(),
				&workflowservice.GetSystemInfoRequest{},
			)
			_ = conn.Close()
			if err != nil {
				lastErr = err
			} else {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("failed waiting, last error: %w", lastErr)
}

func (t *testGRPCServer) GetSystemInfo(
	ctx context.Context,
	req *workflowservice.GetSystemInfoRequest,
) (*workflowservice.GetSystemInfoResponse, error) {
	t.getSystemInfoRequestContext = ctx
	return &t.getSystemInfoResponse, t.getSystemInfoResponseError
}

func (t *testGRPCServer) SignalWorkflowExecution(
	context.Context,
	*workflowservice.SignalWorkflowExecutionRequest,
) (*workflowservice.SignalWorkflowExecutionResponse, error) {
	atomic.AddInt32(&t.sigWfCount, 1)
	return &t.signalWorkflowExecutionResponse, t.signalWorkflowExecutionResponseError
}

func (t *testGRPCServer) signalWorkflowInvokeCount() int {
	return int(atomic.LoadInt32(&t.sigWfCount))
}

func (t *testGRPCServer) resetSignalWorkflowInvokeCount() {
	atomic.StoreInt32(&t.sigWfCount, 0)
}
