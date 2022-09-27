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
	"errors"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// UpdateWorkerBuildIDOrderingOptions is the input to Client.UpdateWorkerBuildIDOrdering.
type UpdateWorkerBuildIDOrderingOptions struct {
	// The task queue to update the version sets of.
	TaskQueue string
	// Required, indicates the build id being added or targeted.
	WorkerBuildID string
	// May be empty, and if set, indicates an existing version the new id should be considered compatible with.
	CompatibleVersion string
	// If true, this new id will become the default version for new workflow executions.
	BecomeDefault bool
}

// Validates and converts the user's options into the proto request. Namespace must be attached afterward.
func (uw *UpdateWorkerBuildIDOrderingOptions) validateAndConvertToProto() (*workflowservice.UpdateWorkerBuildIdOrderingRequest, error) {
	if uw.TaskQueue == "" {
		return nil, errors.New("TaskQueue is required")
	}
	if uw.WorkerBuildID == "" {
		return nil, errors.New("WorkerBuildID is required")
	}
	req := &workflowservice.UpdateWorkerBuildIdOrderingRequest{
		TaskQueue: uw.TaskQueue,
	}
	if uw.CompatibleVersion != "" {
		req.Operation = &workflowservice.UpdateWorkerBuildIdOrderingRequest_NewCompatibleVersion_{
			NewCompatibleVersion: &workflowservice.UpdateWorkerBuildIdOrderingRequest_NewCompatibleVersion{
				NewVersionId:              uw.WorkerBuildID,
				ExistingCompatibleVersion: uw.CompatibleVersion,
				BecomeDefault:             uw.BecomeDefault,
			},
		}
	} else if uw.BecomeDefault {
		req.Operation = &workflowservice.UpdateWorkerBuildIdOrderingRequest_ExistingVersionIdInSetToPromote{
			ExistingVersionIdInSetToPromote: uw.WorkerBuildID,
		}
	} else {
		req.Operation = &workflowservice.UpdateWorkerBuildIdOrderingRequest_NewDefaultVersionId{
			NewDefaultVersionId: uw.WorkerBuildID,
		}
	}

	return req, nil
}

type GetWorkerBuildIDOrderingOptions struct {
	TaskQueue string
	MaxSets   int
}

// WorkerBuildIDVersionSets is the response for Client.GetWorkerBuildIdOrdering and represents the sets
// of worker build id based versions.
type WorkerBuildIDVersionSets struct {
	Sets []*CompatibleVersionSet
}

// Default returns the current overall default version. IE: The one that will be used to start new workflows.
// Returns the empty string if there are no versions present.
func (s *WorkerBuildIDVersionSets) Default() string {
	if len(s.Sets) == 0 {
		return ""
	}
	lastSet := s.Sets[len(s.Sets)-1]
	if len(lastSet.Versions) == 0 {
		return ""
	}
	return lastSet.Versions[len(lastSet.Versions)-1]
}

// CompatibleVersionSet represents a set of worker build ids which are compatible with each other.
type CompatibleVersionSet struct {
	id       string
	Versions []string
}

func workerVersionSetsFromProtoResponse(response *workflowservice.GetWorkerBuildIdOrderingResponse) *WorkerBuildIDVersionSets {
	if response == nil {
		return nil
	}
	return &WorkerBuildIDVersionSets{
		Sets: workerVersionSetsFromProto(response.GetMajorVersionSets()),
	}
}

func workerVersionSetsFromProto(sets []*taskqueuepb.CompatibleVersionSet) []*CompatibleVersionSet {
	if sets == nil {
		return nil
	}
	result := make([]*CompatibleVersionSet, len(sets))
	for i, s := range sets {
		result[i] = &CompatibleVersionSet{
			id:       s.GetId(),
			Versions: s.GetVersions(),
		}
	}
	return result
}
