package internal

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

func TestLoadServerCapabilities(t *testing.T) {
	client := &capServiceClient{}

	client.err = fmt.Errorf("call fail!")
	_, err := loadServerCapabilities(client)
	require.Error(t, err)

	client.err = nil
	client.info = &workflowservice.GetClusterInfoResponse{ServerVersion: "bad"}
	_, err = loadServerCapabilities(client)
	require.Error(t, err)

	client.info.ServerVersion = "1.2.3-beta"
	cap, err := loadServerCapabilities(client)
	require.NoError(t, err)
	require.False(t, cap.SignalHeader)
	require.False(t, cap.QueryHeader)

	client.info.ServerVersion = "1.15.1-beta"
	cap, err = loadServerCapabilities(client)
	require.NoError(t, err)
	require.True(t, cap.SignalHeader)
	require.True(t, cap.QueryHeader)
}

type capServiceClient struct {
	workflowservice.WorkflowServiceClient
	info *workflowservice.GetClusterInfoResponse
	err  error
}

func (c *capServiceClient) GetClusterInfo(
	ctx context.Context,
	in *workflowservice.GetClusterInfoRequest,
	opts ...grpc.CallOption,
) (*workflowservice.GetClusterInfoResponse, error) {
	return c.info, c.err
}

func TestSemVer(t *testing.T) {
	assertSemVer(t, "1.2.3", 1, 2, 3)
	assertSemVer(t, "1.2.3-beta", 1, 2, 3)
	assertSemVer(t, "1.2.3+beta", 1, 2, 3)
	assertSemVer(t, "v1.2.3", 1, 2, 3)

	assertSemVerFail(t, "")
	assertSemVerFail(t, "1.2")
	assertSemVerFail(t, "1.2.")
	assertSemVerFail(t, "1.2.3.4")
	assertSemVerFail(t, "1.2.3_beta")
	assertSemVerFail(t, "u1.2.3")
}

func assertSemVer(t *testing.T, str string, major, minor, patch int) {
	s, err := parseSemVer(str)
	require.NoError(t, err, str)
	require.Equal(t, major, s.major, str)
	require.Equal(t, minor, s.minor, str)
	require.Equal(t, patch, s.patch, str)
}

func assertSemVerFail(t *testing.T, str string) {
	_, err := parseSemVer(str)
	require.Error(t, err, str)
}
