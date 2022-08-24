package internal

import (
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// WorkerBuildIDVersionGraph is the response for Client.GetWorkerBuildIdOrdering and represents the graph
// of worker build id based versions.
type WorkerBuildIDVersionGraph struct {
	// The currently established default version
	CurrentDefault *WorkerVersionIDNode
	// Other current latest-compatible versions which are not the overall default
	CompatibleLeaves []*WorkerVersionIDNode
}
type WorkerVersionIDNode struct {
	WorkerBuildID string
	// A pointer to the previous version this version is considered to be compatible with
	PreviousCompatible *WorkerVersionIDNode
	// A pointer to the previous incompatible version (previous major version)
	PreviousIncompatible *WorkerVersionIDNode
}

func fromProtoResponse(response *workflowservice.GetWorkerBuildIdOrderingResponse) *WorkerBuildIDVersionGraph {
	if response == nil {
		return nil
	}
	return &WorkerBuildIDVersionGraph{
		CurrentDefault:   fromProtoNode(response.CurrentDefault),
		CompatibleLeaves: fromProtoNodes(response.CompatibleLeaves),
	}
}

func fromProtoNode(node *taskqueuepb.VersionIdNode) *WorkerVersionIDNode {
	if node == nil {
		return nil
	}
	return &WorkerVersionIDNode{
		WorkerBuildID:        node.GetVersion().GetWorkerBuildId(),
		PreviousCompatible:   fromProtoNode(node.PreviousCompatible),
		PreviousIncompatible: fromProtoNode(node.PreviousIncompatible),
	}
}

func fromProtoNodes(nodes []*taskqueuepb.VersionIdNode) []*WorkerVersionIDNode {
	if nodes == nil {
		return nil
	}
	result := make([]*WorkerVersionIDNode, len(nodes))
	for i, node := range nodes {
		result[i] = fromProtoNode(node)
	}
	return result
}
