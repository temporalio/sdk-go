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

package test

import (
	"os"
	"strings"

	"google.golang.org/grpc"

	"github.com/temporalio/temporal-proto/workflowservice"
)

// Config contains the integration test configuration
type Config struct {
	ServiceAddr string
	IsStickyOff bool
	Debug       bool
}

func newConfig() Config {
	cfg := Config{
		ServiceAddr: "127.0.0.1:7233",
		IsStickyOff: true,
	}
	if addr := getEnvServiceAddr(); addr != "" {
		cfg.ServiceAddr = addr
	}
	if so := getEnvStickyOff(); so != "" {
		cfg.IsStickyOff = so == "true"
	}
	if debug := getDebug(); debug != "" {
		cfg.Debug = debug == "true"
	}
	return cfg
}

func getEnvServiceAddr() string {
	return strings.TrimSpace(os.Getenv("SERVICE_ADDR"))
}

func getEnvStickyOff() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("STICKY_OFF")))
}

func getDebug() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("DEBUG")))
}

type rpcClient struct {
	workflowservice.WorkflowServiceClient
	connectionCloser func() error
}

func (c *rpcClient) Close() {
	_ = c.connectionCloser()
}

// newRPCClient builds and returns a new rpc client that is able to
// make calls to the localhost temporal-server container
func newRPCClient(serviceAddr string) (*rpcClient, error) {
	connection, err := grpc.Dial(serviceAddr, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	client := workflowservice.NewWorkflowServiceClient(connection)
	return &rpcClient{WorkflowServiceClient: client, connectionCloser: func() error { return connection.Close() }}, nil
}
