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
