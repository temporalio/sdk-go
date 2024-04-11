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

package test_test

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type WorkerVersioningTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	activities *Activities
}

func TestWorkerVersioningTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerVersioningTestSuite))
}

func (ts *WorkerVersioningTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = &Activities{}
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerVersioningTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerVersioningTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerVersioningTestSuite) TestManipulateVersionSets() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()
	err := ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: "1.0",
		},
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: "2.0",
		},
	})
	ts.NoError(err)
	err = ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewCompatibleVersion{
			BuildID:                   "1.1",
			ExistingCompatibleBuildID: "1.0",
		},
	})
	ts.NoError(err)

	res, err := ts.client.GetWorkerBuildIdCompatibility(ctx, &client.GetWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)
	ts.Equal("2.0", res.Default())
	ts.Equal("1.1", res.Sets[0].BuildIDs[1])
	ts.Equal("1.0", res.Sets[0].BuildIDs[0])
}

func (ts *WorkerVersioningTestSuite) TestManipulateRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
				Ramp: &client.VersioningRampByPercentage{
					Percentage: 45.0,
				},
			},
		},
	})
	ts.NoError(err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpAddRedirectRule{
			Rule: client.VersioningRedirectRule{
				SourceBuildID: "1.0",
				TargetBuildID: "2.0",
			},
		},
	})
	ts.NoError(err)

	res, err = ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	ts.Equal(res, resp)

	ts.Equal("2.0", res.AssignmentRules[0].Rule.TargetBuildID)
	r, ok := res.AssignmentRules[0].Rule.Ramp.(*client.VersioningRampByPercentage)
	ts.Truef(ok, "Not a percentage ramp")
	ts.Equal(float32(45.0), r.Percentage)

	ts.Equal("1.0", res.AssignmentRules[1].Rule.TargetBuildID)

	ts.Equal("1.0", res.RedirectRules[0].Rule.SourceBuildID)
	ts.Equal("2.0", res.RedirectRules[0].Rule.TargetBuildID)
}

func (ts *WorkerVersioningTestSuite) TestReplaceDeleteRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	// Replace by unconditional rule is OK
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpReplaceAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.1",
			},
		},
	})
	ts.NoError(err)

	// Replace by conditional rule fails
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpReplaceAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
				Ramp: &client.VersioningRampByPercentage{
					Percentage: 45.0,
				},
			},
			Force: false,
		},
	})
	// Without `force` at least one unconditional rule should remain
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpReplaceAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
				Ramp: &client.VersioningRampByPercentage{
					Percentage: 45.0,
				},
			},
			Force: true,
		},
	})
	ts.NoError(err)

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpDeleteAssignmentRule{
			RuleIndex: 0,
			Force:     false,
		},
	})
	// At least one unconditional rule left without `force`
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpDeleteAssignmentRule{
			RuleIndex: 0,
			Force:     true,
		},
	})
	ts.NoError(err)
}

func (ts *WorkerVersioningTestSuite) TestCommitRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
				Ramp: &client.VersioningRampByPercentage{
					Percentage: 45.0,
				},
			},
		},
	})
	ts.NoError(err)

	// No worker recently polling so it should fail
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpCommitBuildID{
			TargetBuildID: "2.0",
			Force:         false,
		},
	})
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	// Use the `force`...
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpCommitBuildID{
			TargetBuildID: "2.0",
			Force:         true,
		},
	})
	ts.NoError(err)

	res, err = ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	ts.Equal(res, resp)

	// replace all rules by unconditional "2.0"
	ts.Equal(1, len(res.AssignmentRules))
	ts.Equal("2.0", res.AssignmentRules[0].Rule.TargetBuildID)
	_, ok := res.AssignmentRules[0].Rule.Ramp.(*client.VersioningRampByPercentage)
	ts.Falsef(ok, "Still has a percentage ramp")
}

func (ts *WorkerVersioningTestSuite) TestConflictTokens() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken, // duplicated token
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
			},
		},
	})
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken, // original token
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
			},
		},
	})
	ts.NoError(err)
}

func (ts *WorkerVersioningTestSuite) TestTwoWorkersGetDifferentTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	err := ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: "1.0",
		},
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	// Start some workflows targeting 1.0
	handle11, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle12, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// Now add the 2.0 version
	err = ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: "2.0",
		},
	})
	ts.NoError(err)
	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "2.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker2)
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// If we add the worker before the BuildID "2.0" has been registered, the worker poller ends up
	// in the new versioning queue, and it only recovers after 1m timeout.
	worker3 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "2.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker3)
	ts.NoError(worker3.Start())
	defer worker3.Stop()

	// 2.0 workflows
	handle21, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle22, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// finish them all
	ts.NoError(ts.client.SignalWorkflow(ctx, handle11.GetID(), handle11.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle12.GetID(), handle12.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle21.GetID(), handle21.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle22.GetID(), handle22.GetRunID(), "start-signal", ""))

	// Wait for all wfs to finish
	ts.NoError(handle11.Get(ctx, nil))
	ts.NoError(handle12.Get(ctx, nil))
	ts.NoError(handle21.Get(ctx, nil))
	ts.NoError(handle22.Get(ctx, nil))
}

func (ts *WorkerVersioningTestSuite) TestTwoWorkersGetDifferentTasksWithRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()
	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "2.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker2)
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// Start some workflows targeting 1.0
	handle11, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle12, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// Now add the 2.0 version
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
			},
		},
	})
	ts.NoError(err)

	// 2.0 workflows
	handle21, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle22, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	// finish them all
	ts.NoError(ts.client.SignalWorkflow(ctx, handle11.GetID(), handle11.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle12.GetID(), handle12.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle21.GetID(), handle21.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle22.GetID(), handle22.GetRunID(), "start-signal", ""))

	// Wait for all wfs to finish
	ts.NoError(handle11.Get(ctx, nil))
	ts.NoError(handle12.Get(ctx, nil))
	ts.NoError(handle21.Get(ctx, nil))
	ts.NoError(handle22.Get(ctx, nil))
}

func (ts *WorkerVersioningTestSuite) TestReachabilityUnreachable() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	buildID := uuid.New()
	compatibility, err := ts.client.GetWorkerTaskReachability(ctx, &client.GetWorkerTaskReachabilityOptions{
		BuildIDs:   []string{buildID},
		TaskQueues: []string{ts.taskQueueName},
	})
	ts.NoError(err)
	ts.Equal(1, len(compatibility.BuildIDReachability))
	buildIDReachable, ok := compatibility.BuildIDReachability[buildID]
	ts.True(ok)
	ts.Equal(1, len(buildIDReachable.TaskQueueReachable))
	taskQueueReachable, ok := buildIDReachable.TaskQueueReachable[ts.taskQueueName]
	ts.True(ok)
	ts.Equal(0, len(taskQueueReachable.TaskQueueReachability))
}

func (ts *WorkerVersioningTestSuite) TestReachabilityUnversionedWorker() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	compatibility, err := ts.client.GetWorkerTaskReachability(ctx, &client.GetWorkerTaskReachabilityOptions{
		BuildIDs:   []string{client.UnversionedBuildID},
		TaskQueues: []string{ts.taskQueueName},
	})

	ts.NoError(err)
	ts.Equal(1, len(compatibility.BuildIDReachability))
	buildIDReachable, ok := compatibility.BuildIDReachability[client.UnversionedBuildID]
	ts.True(ok)
	ts.Equal(1, len(buildIDReachable.TaskQueueReachable))
	taskQueueReachable, ok := buildIDReachable.TaskQueueReachable[ts.taskQueueName]
	ts.True(ok)
	ts.Equal([]client.TaskReachability{client.TaskReachabilityNewWorkflows}, taskQueueReachable.TaskQueueReachability)
}

func (ts *WorkerVersioningTestSuite) TestReachabilityVersions() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	buildID1 := uuid.New()
	buildID2 := uuid.New()

	err := ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: buildID1,
		},
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: buildID1, UseBuildIDForVersioning: true})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	// Start some workflows targeting 1.0
	handle11, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-1"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)
	handle12, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1-2"), ts.workflows.WaitSignalToStart)
	ts.NoError(err)

	ts.NoError(ts.client.SignalWorkflow(ctx, handle11.GetID(), handle11.GetRunID(), "start-signal", ""))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle12.GetID(), handle12.GetRunID(), "start-signal", ""))

	// Wait for all wfs to finish
	ts.NoError(handle11.Get(ctx, nil))
	ts.NoError(handle12.Get(ctx, nil))

	// Start the second worker
	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: buildID2, UseBuildIDForVersioning: true})
	ts.workflows.register(worker2)
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// Now add the 2.0 version
	err = ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: buildID2,
		},
	})
	ts.NoError(err)
	time.Sleep(15 * time.Second)

	compatibility, err := ts.client.GetWorkerTaskReachability(ctx, &client.GetWorkerTaskReachabilityOptions{
		BuildIDs:     []string{buildID1, buildID2},
		TaskQueues:   []string{ts.taskQueueName},
		Reachability: client.TaskReachabilityClosedWorkflows,
	})
	ts.NoError(err)
	ts.Equal(2, len(compatibility.BuildIDReachability))

	// Test the first worker
	buildIDReachability, ok := compatibility.BuildIDReachability[buildID1]
	ts.True(ok)
	ts.Equal(0, len(buildIDReachability.UnretrievedTaskQueues))
	ts.Equal(1, len(buildIDReachability.TaskQueueReachable))
	taskQueueReachability, ok := buildIDReachability.TaskQueueReachable[ts.taskQueueName]
	ts.True(ok)
	ts.Equal(2, len(taskQueueReachability.TaskQueueReachability))
	ts.Equal([]client.TaskReachability{client.TaskReachabilityNewWorkflows, client.TaskReachabilityClosedWorkflows}, taskQueueReachability.TaskQueueReachability)

	// Test the second worker
	buildIDReachability, ok = compatibility.BuildIDReachability[buildID2]
	ts.True(ok)
	ts.Equal(0, len(buildIDReachability.UnretrievedTaskQueues))
	ts.Equal(1, len(buildIDReachability.TaskQueueReachable))
	taskQueueReachability, ok = buildIDReachability.TaskQueueReachable[ts.taskQueueName]
	ts.True(ok)
	ts.Equal(1, len(taskQueueReachability.TaskQueueReachability))
	ts.Equal([]client.TaskReachability{client.TaskReachabilityNewWorkflows}, taskQueueReachability.TaskQueueReachability)
}

func (ts *WorkerVersioningTestSuite) TestBuildIDChangesOverWorkflowLifetime() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	err := ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewIDInNewDefaultSet{
			BuildID: "1.0",
		},
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker1)
	ts.activities.register(worker1)
	ts.NoError(worker1.Start())

	// Start workflow
	wfHandle, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("evolving-wf"), ts.workflows.BuildIDWorkflow)
	// Query to see that the build ID is 1.0
	res, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	var lastBuildID string
	ts.NoError(err)
	ts.NoError(res.Get(&lastBuildID))
	ts.Equal("1.0", lastBuildID)

	// Make sure we've got to the activity
	ts.Eventually(func() bool {
		var didRun bool
		res, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "activity-ran", nil)
		ts.NoError(err)
		ts.NoError(res.Get(&didRun))
		return didRun
	}, time.Second*10, time.Millisecond*100)
	worker1.Stop()

	// Add new compat ver
	err = ts.client.UpdateWorkerBuildIdCompatibility(ctx, &client.UpdateWorkerBuildIdCompatibilityOptions{
		TaskQueue: ts.taskQueueName,
		Operation: &client.BuildIDOpAddNewCompatibleVersion{
			BuildID:                   "1.1",
			ExistingCompatibleBuildID: "1.0",
		},
	})
	ts.NoError(err)

	worker11 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.1", UseBuildIDForVersioning: true})
	ts.workflows.register(worker11)
	ts.activities.register(worker11)
	ts.NoError(worker11.Start())
	defer worker11.Stop()

	_, err = ts.client.WorkflowService().ResetStickyTaskQueue(ctx, &workflowservice.ResetStickyTaskQueueRequest{
		Namespace: ts.config.Namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wfHandle.GetID(),
		},
	})
	ts.NoError(err)

	// The current task, with the new worker, should still be 1.0 since no new tasks have happened
	enval, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	ts.NoError(err)
	ts.NoError(enval.Get(&lastBuildID))
	ts.Equal("1.0", lastBuildID)

	// finish the workflow under 1.1
	ts.NoError(ts.client.SignalWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "finish", ""))
	ts.NoError(wfHandle.Get(ctx, nil))

	// Post completion it should have the value of the last task
	enval, err = ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	ts.NoError(err)
	ts.NoError(enval.Get(&lastBuildID))
	ts.Equal("1.1", lastBuildID)
}

func (ts *WorkerVersioningTestSuite) TestBuildIDChangesOverWorkflowLifetimeWithRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	result, err := ts.client.GetWorkerVersioningRules(ctx, &client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: result.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.0", UseBuildIDForVersioning: true})
	ts.workflows.register(worker1)
	ts.activities.register(worker1)
	ts.NoError(worker1.Start())

	// Start workflow
	wfHandle, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("evolving-wf"), ts.workflows.BuildIDWorkflow)
	// Query to see that the build ID is 1.0
	res, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	var lastBuildID string
	ts.NoError(err)
	ts.NoError(res.Get(&lastBuildID))
	ts.Equal("1.0", lastBuildID)

	// Make sure we've got to the activity
	ts.Eventually(func() bool {
		var didRun bool
		res, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "activity-ran", nil)
		ts.NoError(err)
		ts.NoError(res.Get(&didRun))
		return didRun
	}, time.Second*10, time.Millisecond*100)
	worker1.Stop()

	// Add new compat ver
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.1",
			},
		},
	})

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, &client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOpAddRedirectRule{
			Rule: client.VersioningRedirectRule{
				SourceBuildID: "1.0",
				TargetBuildID: "1.1",
			},
		},
	})
	ts.NoError(err)

	worker11 := worker.New(ts.client, ts.taskQueueName, worker.Options{BuildID: "1.1", UseBuildIDForVersioning: true})
	ts.workflows.register(worker11)
	ts.activities.register(worker11)
	ts.NoError(worker11.Start())
	defer worker11.Stop()

	_, err = ts.client.WorkflowService().ResetStickyTaskQueue(ctx, &workflowservice.ResetStickyTaskQueueRequest{
		Namespace: ts.config.Namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wfHandle.GetID(),
		},
	})
	ts.NoError(err)

	// The current task, with the new worker, should still be 1.0 since no new tasks have happened
	enval, err := ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	ts.NoError(err)
	ts.NoError(enval.Get(&lastBuildID))
	ts.Equal("1.0", lastBuildID)

	// finish the workflow under 1.1
	ts.NoError(ts.client.SignalWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "finish", ""))
	ts.NoError(wfHandle.Get(ctx, nil))

	// Post completion it should have the value of the last task
	enval, err = ts.client.QueryWorkflow(ctx, wfHandle.GetID(), wfHandle.GetRunID(), "get-last-build-id", nil)
	ts.NoError(err)
	ts.NoError(enval.Get(&lastBuildID))
	ts.Equal("1.1", lastBuildID)
}
