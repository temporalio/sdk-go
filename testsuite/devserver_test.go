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
	defer server.Stop()
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_SpecificVersion(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{CachedDownload: testsuite.CachedDownload{Version: "v0.3.0"}})
	require.NoError(t, err)
	defer server.Stop()
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_CustomNamespace(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{ClientOptions: &client.Options{Namespace: "testing"}})
	require.NoError(t, err)
	defer server.Stop()
	info, err := server.Client().WorkflowService().DescribeNamespace(context.Background(), &workflowservice.DescribeNamespaceRequest{Namespace: "testing"})
	require.NoError(t, err)
	require.Equal(t, "testing", info.NamespaceInfo.Name)
}
