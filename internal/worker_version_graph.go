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

// UpdateWorkerBuildIDOrderingOptions is the input to Client.UpdateWorkerBuildIDOrdering.
type UpdateWorkerBuildIDOrderingOptions struct {
	// The task queue to update the version graph of.
	TaskQueue string
	// Required, indicates the build id being added (or changed) to/in the graph.
	WorkerBuildID string
	// May be empty, and if set, indicates an existing version the new id should be considered compatible with.
	PreviousCompatible string
	// If true, this new id will become the default version for new workflow executions.
	BecomeDefault bool
}

type GetWorkerBuildIDOrderingOptions struct {
	TaskQueue string
	MaxDepth  int
}

// WorkerBuildIDVersionGraph is the response for Client.GetWorkerBuildIdOrdering and represents the graph
// of worker build id based versions.
type WorkerBuildIDVersionGraph struct {
	// The currently established default version
	Default *WorkerVersionIDNode
	// Other current latest-compatible versions which are not the overall default
	CompatibleLeaves []*WorkerVersionIDNode
}

// WorkerVersionIDNode is a single node in a worker version graph.
type WorkerVersionIDNode struct {
	WorkerBuildID string
	// A pointer to the previous version this version is considered to be compatible with
	PreviousCompatible *WorkerVersionIDNode
	// A pointer to the previous incompatible version (previous major version)
	PreviousIncompatible *WorkerVersionIDNode
}

func workerVersionGraphFromProtoResponse(response *workflowservice.GetWorkerBuildIdOrderingResponse) *WorkerBuildIDVersionGraph {
	if response == nil {
		return nil
	}
	return &WorkerBuildIDVersionGraph{
		Default:          workerVersionNodeFromProto(response.CurrentDefault),
		CompatibleLeaves: workerVersionNodesFromProto(response.CompatibleLeaves),
	}
}

func workerVersionNodeFromProto(node *taskqueuepb.VersionIdNode) *WorkerVersionIDNode {
	if node == nil {
		return nil
	}
	return &WorkerVersionIDNode{
		WorkerBuildID:        node.GetVersion().GetWorkerBuildId(),
		PreviousCompatible:   workerVersionNodeFromProto(node.PreviousCompatible),
		PreviousIncompatible: workerVersionNodeFromProto(node.PreviousIncompatible),
	}
}

func workerVersionNodesFromProto(nodes []*taskqueuepb.VersionIdNode) []*WorkerVersionIDNode {
	if len(nodes) == 0 {
		return nil
	}
	result := make([]*WorkerVersionIDNode, len(nodes))
	for i, node := range nodes {
		result[i] = workerVersionNodeFromProto(node)
	}
	return result
}
