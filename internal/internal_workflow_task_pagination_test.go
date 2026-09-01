package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
)

func paginationTestCommand(payloadSize int) *commandpb.Command {
	return &commandpb.Command{
		CommandType: enumspb.COMMAND_TYPE_RECORD_MARKER,
		Attributes: &commandpb.Command_RecordMarkerCommandAttributes{
			RecordMarkerCommandAttributes: &commandpb.RecordMarkerCommandAttributes{
				MarkerName: "test",
				Details: map[string]*commonpb.Payloads{
					"data": {Payloads: []*commonpb.Payload{{Data: make([]byte, payloadSize)}}},
				},
			},
		},
	}
}

func paginationTestRequest(commands []*commandpb.Command) *workflowservice.RespondWorkflowTaskCompletedRequest {
	return &workflowservice.RespondWorkflowTaskCompletedRequest{
		TaskToken: []byte("task-token"),
		Namespace: "namespace",
		Identity:  "identity",
		Commands:  commands,
	}
}

func TestPaginateWorkflowTaskCompletion_FitsInSinglePage(t *testing.T) {
	request := paginationTestRequest([]*commandpb.Command{paginationTestCommand(16)})
	intermediate, final := paginateWorkflowTaskCompletion(request, 4096)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
	require.False(t, final.IntermediatePage)
	require.EqualValues(t, 0, final.PageNumber)
}

func TestPaginateWorkflowTaskCompletion_SplitsCommandsAcrossPages(t *testing.T) {
	const maxPageBytes = 1024
	const commandCount = 6
	var commands []*commandpb.Command
	for i := 0; i < commandCount; i++ {
		commands = append(commands, paginationTestCommand(400))
	}
	request := paginationTestRequest(commands)
	require.Greater(t, proto.Size(request), maxPageBytes)

	intermediate, final := paginateWorkflowTaskCompletion(request, maxPageBytes)
	require.NotEmpty(t, intermediate)

	// The final page carries no commands and is numbered after every intermediate page.
	require.False(t, final.IntermediatePage)
	require.Empty(t, final.Commands)
	require.EqualValues(t, len(intermediate), final.PageNumber)
	require.LessOrEqual(t, proto.Size(final), maxPageBytes)

	totalCommands := 0
	for i, page := range intermediate {
		require.True(t, page.IntermediatePage)
		require.EqualValues(t, i, page.PageNumber)
		require.Equal(t, []byte("task-token"), page.TaskToken)
		require.LessOrEqual(t, proto.Size(page), maxPageBytes, "intermediate page %d over limit", i)
		totalCommands += len(page.Commands)
	}
	// Every command is preserved exactly once across the intermediate pages.
	require.Equal(t, commandCount, totalCommands)
}

func TestPaginateWorkflowTaskCompletion_SingleCommandLargerThanPageIsNotSplit(t *testing.T) {
	request := paginationTestRequest([]*commandpb.Command{paginationTestCommand(4096)})
	intermediate, final := paginateWorkflowTaskCompletion(request, 1024)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
}

func TestPaginateWorkflowTaskCompletion_NoCommandsIsNotSplit(t *testing.T) {
	// Over the limit because of messages, but with no commands there is nothing to distribute.
	request := paginationTestRequest(nil)
	request.Messages = []*protocolpb.Message{{Id: string(make([]byte, 2048))}}
	intermediate, final := paginateWorkflowTaskCompletion(request, 1024)
	require.Empty(t, intermediate)
	require.Same(t, request, final)
}
