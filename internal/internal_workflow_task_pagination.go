package internal

import (
	"sync/atomic"
	"time"

	commandpb "go.temporal.io/api/command/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
)

// workflowTaskCompletionPaginationConfig holds the namespace-resolved settings governing pagination
// of oversized workflow task completions. It is resolved once when the worker starts and read while
// completing tasks, so the worker and its pollers share it by pointer.
type workflowTaskCompletionPaginationConfig struct {
	// enabled is true when the namespace advertises the workflow_task_completion_pagination capability.
	enabled atomic.Bool
	// sizeLimit is the namespace's limit on the recombined completion size; a completion over it is
	// rejected server-side, so the worker fails it proactively. Zero means no limit was advertised.
	sizeLimit atomic.Int64
}

const (
	// maxWorkflowTaskCompletionPageBytes is the maximum encoded size of a single completion page,
	// kept below the ~4 MiB gRPC frame limit. This per-page cap is distinct from the namespace's
	// limit on the recombined completion size.
	//
	// Pages are packed by summing command body sizes only; the 512 KiB of headroom below 4 MiB
	// absorbs everything that sum omits: the per-request overhead (task token, identity, namespace)
	// and the per-command wire framing (a field tag plus a length varint, up to 6 bytes each). At the
	// server's default per-workflow history-count limit (~51,200 events), worst-case framing is
	// ~300 KiB, so this headroom covers even a page of many tiny commands and lets us skip
	// per-command accounting.
	maxWorkflowTaskCompletionPageBytes = 4*1024*1024 - 512*1024
	// Conservative heuristic, not a tuned value: caps the client-side burst (concurrent request
	// bodies and streams); the cost is only extra serial rounds for completions over this many pages.
	maxConcurrentWorkflowTaskCompletionPages = 3

	workflowTaskCompletionPageResendInitialBackoff = 100 * time.Millisecond
	workflowTaskCompletionPageResendMaxBackoff     = 5 * time.Second
)

// paginateWorkflowTaskCompletion splits a completion that exceeds maxPageBytes into intermediate
// pages that each stay under it, by distributing its commands across them in order. The server
// buffers only the commands of intermediate pages, so all messages and metadata ride on the
// returned final page, whose PageNumber is the count of intermediate pages.
//
// It returns (nil, request) — meaning send the request as-is — when the request already fits, has
// no commands to distribute, or has a single command that alone exceeds a page (which the server
// then rejects).
func paginateWorkflowTaskCompletion(
	request *workflowservice.RespondWorkflowTaskCompletedRequest,
	maxPageBytes int,
) (intermediatePages []*workflowservice.RespondWorkflowTaskCompletedRequest, finalPage *workflowservice.RespondWorkflowTaskCompletedRequest) {
	if proto.Size(request) <= maxPageBytes {
		return nil, request
	}

	newIntermediatePage := func(commands []*commandpb.Command, pageNumber int) *workflowservice.RespondWorkflowTaskCompletedRequest {
		return &workflowservice.RespondWorkflowTaskCompletedRequest{
			TaskToken:        request.TaskToken,
			Identity:         request.Identity,
			Namespace:        request.Namespace,
			IntermediatePage: true,
			PageNumber:       int32(pageNumber),
			Commands:         commands,
		}
	}

	// Pages are packed purely by command body size; maxPageBytes reserves headroom for the
	// per-request and per-command overhead this ignores. Only commands can be split across pages, so
	// pagination cannot help when there are none, or when a single command alone exceeds a page.
	if len(request.Commands) == 0 {
		return nil, request
	}
	for _, command := range request.Commands {
		if proto.Size(command) > maxPageBytes {
			return nil, request
		}
	}

	var current []*commandpb.Command
	currentLen := 0
	for _, command := range request.Commands {
		commandLen := proto.Size(command)
		if len(current) > 0 && currentLen+commandLen > maxPageBytes {
			intermediatePages = append(intermediatePages, newIntermediatePage(current, len(intermediatePages)))
			current = nil
			currentLen = 0
		}
		currentLen += commandLen
		current = append(current, command)
	}
	if len(current) > 0 {
		intermediatePages = append(intermediatePages, newIntermediatePage(current, len(intermediatePages)))
	}

	request.Commands = nil
	request.PageNumber = int32(len(intermediatePages))
	request.IntermediatePage = false
	return intermediatePages, request
}
