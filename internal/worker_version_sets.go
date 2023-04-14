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

// UpdateWorkerBuildIdCompatibilityOptions is the input to Client.UpdateWorkerBuildIdCompatibility.
type UpdateWorkerBuildIdCompatibilityOptions struct {
	// The task queue to update the version sets of.
	TaskQueue string
	// Required, indicates the build id being added or targeted.
	WorkerBuildID string
	// May be empty, and if set, indicates an existing version the new id should be considered compatible with.
	CompatibleBuildID string
	// If true, this new id will become the default version for new workflow executions.
	BecomeDefault bool
}

// Validates and converts the user's options into the proto request. Namespace must be attached afterward.
func (uw *UpdateWorkerBuildIdCompatibilityOptions) validateAndConvertToProto() (*workflowservice.UpdateWorkerBuildIdCompatibilityRequest, error) {
	if uw.TaskQueue == "" {
		return nil, errors.New("TaskQueue is required")
	}
	if uw.WorkerBuildID == "" {
		return nil, errors.New("WorkerBuildID is required")
	}
	req := &workflowservice.UpdateWorkerBuildIdCompatibilityRequest{
		TaskQueue: uw.TaskQueue,
	}
	if uw.CompatibleBuildID != "" {
		req.Operation = &workflowservice.UpdateWorkerBuildIdCompatibilityRequest_AddNewCompatibleBuildId{
			AddNewCompatibleBuildId: &workflowservice.UpdateWorkerBuildIdCompatibilityRequest_AddNewCompatibleVersion{
				NewBuildId:                uw.WorkerBuildID,
				ExistingCompatibleBuildId: uw.CompatibleBuildID,
				MakeSetDefault:            uw.BecomeDefault,
			},
		}
	} else if uw.BecomeDefault {
		req.Operation = &workflowservice.UpdateWorkerBuildIdCompatibilityRequest_PromoteSetByBuildId{
			PromoteSetByBuildId: uw.WorkerBuildID,
		}
	} else {
		req.Operation = &workflowservice.UpdateWorkerBuildIdCompatibilityRequest_AddNewBuildIdInNewDefaultSet{
			AddNewBuildIdInNewDefaultSet: uw.WorkerBuildID,
		}
	}

	return req, nil
}

type GetWorkerBuildIdCompatibilityOptions struct {
	TaskQueue string
	MaxSets   int
}

// WorkerBuildIDVersionSets is the response for Client.GetWorkerBuildIdCompatibility and represents the sets
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
	if len(lastSet.BuildIDs) == 0 {
		return ""
	}
	return lastSet.BuildIDs[len(lastSet.BuildIDs)-1]
}

// CompatibleVersionSet represents a set of worker build ids which are compatible with each other.
type CompatibleVersionSet struct {
	BuildIDs []string
}

func workerVersionSetsFromProtoResponse(response *workflowservice.GetWorkerBuildIdCompatibilityResponse) *WorkerBuildIDVersionSets {
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
			BuildIDs: s.GetBuildIds(),
		}
	}
	return result
}
