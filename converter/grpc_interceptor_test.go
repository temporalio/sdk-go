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

package converter

import (
	"context"
	"fmt"
	"log"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var zlibDataConverter = NewEncodingDataConverter(
	defaultDataConverter,
	NewZlibEncoder(ZlibEncoderOptions{AlwaysEncode: true}),
)

func payloadEncoding(payloads *commonpb.Payloads) string {
	return string(payloads.Payloads[0].Metadata[MetadataEncoding])
}

func TestServiceInterceptorRequests(t *testing.T) {
	require := require.New(t)

	s := serviceInterceptor{
		encoders: []PayloadEncoder{NewZlibEncoder(ZlibEncoderOptions{AlwaysEncode: true})},
	}
	unencodedPayloads, err := defaultDataConverter.ToPayloads("test")
	require.NoError(err)

	startReq := &workflowservice.StartWorkflowExecutionRequest{
		Input: unencodedPayloads,
	}
	err = s.processRequest(startReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(startReq.Input))

	signalReq := &workflowservice.StartWorkflowExecutionRequest{
		Input: unencodedPayloads,
	}
	err = s.processRequest(signalReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(signalReq.Input))

	signalWithStartReq := &workflowservice.SignalWithStartWorkflowExecutionRequest{
		Input:       unencodedPayloads,
		SignalInput: unencodedPayloads,
	}
	err = s.processRequest(signalWithStartReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(signalWithStartReq.Input))
	require.Equal("binary/zlib", payloadEncoding(signalWithStartReq.SignalInput))

	respondActivityCompletedReq := &workflowservice.RespondActivityTaskCompletedRequest{
		Result: unencodedPayloads,
	}
	err = s.processRequest(respondActivityCompletedReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(respondActivityCompletedReq.Result))

	respondActivityCompletedByIdReq := &workflowservice.RespondActivityTaskCompletedByIdRequest{
		Result: unencodedPayloads,
	}
	err = s.processRequest(respondActivityCompletedByIdReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(respondActivityCompletedByIdReq.Result))

	respondWorkflowTaskCompletedReq := &workflowservice.RespondWorkflowTaskCompletedRequest{
		Commands: []*commandpb.Command{
			{
				CommandType: enums.COMMAND_TYPE_COMPLETE_WORKFLOW_EXECUTION,
				Attributes: &commandpb.Command_CompleteWorkflowExecutionCommandAttributes{
					CompleteWorkflowExecutionCommandAttributes: &commandpb.CompleteWorkflowExecutionCommandAttributes{
						Result: unencodedPayloads,
					},
				},
			},
		},
	}
	err = s.processRequest(respondWorkflowTaskCompletedReq)
	require.NoError(err)

	result := respondWorkflowTaskCompletedReq.Commands[0].GetCompleteWorkflowExecutionCommandAttributes().Result
	require.Equal("binary/zlib", payloadEncoding(result))

	recordActivityTaskHeartbeatReq := &workflowservice.RecordActivityTaskHeartbeatRequest{
		Details: unencodedPayloads,
	}
	err = s.processRequest(recordActivityTaskHeartbeatReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(recordActivityTaskHeartbeatReq.Details))

	recordActivityTaskHeartbeatByIdReq := &workflowservice.RecordActivityTaskHeartbeatByIdRequest{
		Details: unencodedPayloads,
	}
	err = s.processRequest(recordActivityTaskHeartbeatByIdReq)
	require.NoError(err)

	require.Equal("binary/zlib", payloadEncoding(recordActivityTaskHeartbeatByIdReq.Details))
}

func TestClientInterceptor(t *testing.T) {
	require := require.New(t)

	server, err := startTestGRPCServer()
	require.NoError(err)

	interceptor, err := NewPayloadEncoderGRPCClientInterceptor(
		PayloadEncoderGRPCClientInterceptorOptions{
			Encoders: []PayloadEncoder{NewZlibEncoder(ZlibEncoderOptions{AlwaysEncode: true})},
		},
	)
	require.NoError(err)

	c, err := grpc.Dial(
		server.addr,
		grpc.WithInsecure(),
		grpc.WithChainUnaryInterceptor(interceptor),
	)
	require.NoError(err)

	client := workflowservice.NewWorkflowServiceClient(c)
	input, err := defaultDataConverter.ToPayloads("test")
	require.NoError(err)

	_, err = client.StartWorkflowExecution(
		context.Background(),
		&workflowservice.StartWorkflowExecutionRequest{
			Input: input,
		},
	)
	require.NoError(err)

	payloads := server.startWorkflowExecutionRequest.Input.Payloads

	require.Equal("binary/zlib", string(payloads[0].Metadata[MetadataEncoding]))

	response, err := client.PollActivityTaskQueue(
		context.Background(),
		&workflowservice.PollActivityTaskQueueRequest{},
	)
	require.NoError(err)

	payloads = response.Input.Payloads

	require.Equal("json/plain", string(payloads[0].Metadata[MetadataEncoding]))

}

type testGRPCServer struct {
	workflowservice.UnimplementedWorkflowServiceServer
	*grpc.Server
	addr                          string
	startWorkflowExecutionRequest *workflowservice.StartWorkflowExecutionRequest
}

func startTestGRPCServer() (*testGRPCServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	t := &testGRPCServer{Server: grpc.NewServer(), addr: l.Addr().String()}
	workflowservice.RegisterWorkflowServiceServer(t.Server, t)
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
		conn, err := grpc.Dial(t.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
		} else {
			_, err := workflowservice.NewWorkflowServiceClient(conn).GetClusterInfo(
				context.Background(),
				&workflowservice.GetClusterInfoRequest{},
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

func (t *testGRPCServer) GetClusterInfo(
	context.Context,
	*workflowservice.GetClusterInfoRequest,
) (*workflowservice.GetClusterInfoResponse, error) {
	return &workflowservice.GetClusterInfoResponse{}, nil
}

func (t *testGRPCServer) StartWorkflowExecution(
	ctx context.Context,
	req *workflowservice.StartWorkflowExecutionRequest,
) (*workflowservice.StartWorkflowExecutionResponse, error) {
	t.startWorkflowExecutionRequest = req
	return &workflowservice.StartWorkflowExecutionResponse{}, nil
}

func (t *testGRPCServer) PollActivityTaskQueue(
	ctx context.Context,
	req *workflowservice.PollActivityTaskQueueRequest,
) (*workflowservice.PollActivityTaskQueueResponse, error) {
	input, err := zlibDataConverter.ToPayloads("test")
	if err != nil {
		return nil, err
	}

	return &workflowservice.PollActivityTaskQueueResponse{
		Input: input,
	}, nil
}
