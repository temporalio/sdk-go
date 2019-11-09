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

	"go.temporal.io/temporal/.gen/go/temporal/workflowserviceclient"
	"go.uber.org/yarpc"
	"go.uber.org/yarpc/transport/tchannel"
)

// Config contains the integration test configuration
type Config struct {
	ServiceAddr string
	ServiceName string
	IsStickyOff bool
	Debug       bool
}

func newConfig() Config {
	cfg := Config{
		ServiceName: "cadence-frontend",
		ServiceAddr: "127.0.0.1:7933",
		IsStickyOff: true,
	}
	if name := getEnvServiceName(); name != "" {
		cfg.ServiceName = name
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

func getEnvServiceName() string {
	return strings.TrimSpace(os.Getenv("SERVICE_NAME"))
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
	workflowserviceclient.Interface
	dispatcher *yarpc.Dispatcher
}

func (c *rpcClient) Close() {
	c.dispatcher.Stop()
}

// newRPCClient builds and returns a new rpc client that is able to
// make calls to the localhost temporal-server container
func newRPCClient(
	serviceName string, serviceAddr string) (*rpcClient, error) {
	transport, err := tchannel.NewTransport(tchannel.ServiceName("integration-test"))
	if err != nil {
		return nil, err
	}
	outbound := transport.NewSingleOutbound(serviceAddr)
	dispatcher := yarpc.NewDispatcher(yarpc.Config{
		Name: "integration-test",
		Outbounds: yarpc.Outbounds{
			serviceName: {
				Unary: outbound,
			},
		},
	})
	if err := dispatcher.Start(); err != nil {
		return nil, err
	}
	client := workflowserviceclient.New(dispatcher.ClientConfig(serviceName))
	return &rpcClient{Interface: client, dispatcher: dispatcher}, nil
}
