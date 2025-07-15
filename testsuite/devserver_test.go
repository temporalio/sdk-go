package testsuite_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"testing"
	"time"
)

func TestStartDevServer_Defaults(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_SpecificVersion(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{CachedDownload: testsuite.CachedDownload{Version: "v0.3.0"}})
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()
	info, err := server.Client().WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_CustomNamespace(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{ClientOptions: &client.Options{Namespace: "testing"}})
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()
	info, err := server.Client().WorkflowService().DescribeNamespace(context.Background(), &workflowservice.DescribeNamespaceRequest{Namespace: "testing"})
	require.NoError(t, err)
	require.Equal(t, "testing", info.NamespaceInfo.Name)
}

func TestStartDevServer_FrontendHostPort(t *testing.T) {
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()
	hostPort := server.FrontendHostPort()
	client, err := client.Dial(client.Options{HostPort: hostPort})
	require.NoError(t, err)
	info, err := client.WorkflowService().GetSystemInfo(context.Background(), &workflowservice.GetSystemInfoRequest{})
	require.NoError(t, err)
	require.NotNil(t, info.Capabilities)
}

func TestStartDevServer_SearchAttributes(t *testing.T) {
	attrBool := temporal.NewSearchAttributeKeyBool("GoTemporalTestBool")
	attrTime := temporal.NewSearchAttributeKeyTime("GoTemporalTestTime")
	attrFloat := temporal.NewSearchAttributeKeyFloat64("GoTemporalTestFloat")
	attrString := temporal.NewSearchAttributeKeyString("GoTemporalTestString")
	attrInt := temporal.NewSearchAttributeKeyInt64("GoTemporalTestInt")
	attrKeyword := temporal.NewSearchAttributeKeyKeyword("GoTemporalTestKeyword")
	attrKeywordList := temporal.NewSearchAttributeKeyKeywordList("GoTemporalTestKeywordList")
	now := time.Now()
	sa := temporal.NewSearchAttributes(
		attrBool.ValueSet(true),
		attrTime.ValueSet(now),
		attrFloat.ValueSet(5.4),
		attrString.ValueSet("string"),
		attrInt.ValueSet(10),
		attrKeyword.ValueSet("keyword"),
		attrKeywordList.ValueSet([]string{"value1", "value2"}),
	)

	opts := testsuite.DevServerOptions{
		SearchAttributes: sa,
	}

	// Confirm that when used in env without SAs it fails
	server, err := testsuite.StartDevServer(context.Background(), testsuite.DevServerOptions{})
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	_, err = server.Client().ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		TypedSearchAttributes: sa,
	}, func(ctx workflow.Context) error { return nil })
	require.Error(t, err)

	// Confirm that when used in env with SAs it succeeds
	server1, err := testsuite.StartDevServer(context.Background(), opts)
	require.NoError(t, err)
	defer func() { _ = server1.Stop() }()

	c := server1.Client()

	run, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		TypedSearchAttributes: sa,
		TaskQueue:             "dev-server-search-attributes-test",
	}, func(ctx workflow.Context) error { return nil })
	require.NoError(t, err)

	describe, err := c.DescribeWorkflow(context.Background(), run.GetID(), run.GetRunID())
	require.NoError(t, err)
	require.Equal(t, describe.TypedSearchAttributes, sa)
}
