// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package testsuite_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

func TestStartDevServer_Defaults(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	require.NoError(t, err)
	defer require.NoError(t, server.Stop())
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_SpecificVersion(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{CachedDownload: testsuite.CachedDownload{Version: "v0.3.0"}})
	require.NoError(t, err)
	defer require.NoError(t, server.Stop())
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_CustomNamespace(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{ClientOptions: &client.Options{Namespace: "testing"}})
	require.NoError(t, err)
	defer require.NoError(t, server.Stop())
	info, err := server.Client().WorkflowService().DescribeNamespace(context.Background(), &workflowservice.DescribeNamespaceRequest{Namespace: "testing"})
	require.NoError(t, err)
	require.Equal(t, "testing", info.NamespaceInfo.Name)
}
