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
	"math"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal"

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
	ts.activities = newActivities()
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

	res, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
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

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationAddRedirectRule{
			Rule: client.VersioningRedirectRule{
				SourceBuildID: "1.0",
				TargetBuildID: "2.0",
			},
		},
	})
	ts.NoError(err)

	res, err = ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
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

	res, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	// Replace by unconditional rule is OK
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationReplaceAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.1",
			},
		},
	})
	ts.NoError(err)

	// Replace by conditional rule fails
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationReplaceAssignmentRule{
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

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationReplaceAssignmentRule{
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

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationDeleteAssignmentRule{
			RuleIndex: 0,
			Force:     false,
		},
	})
	// At least one unconditional rule left without `force`
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationDeleteAssignmentRule{
			RuleIndex: 0,
			Force:     true,
		},
	})
	ts.NoError(err)
}

func (ts *WorkerVersioningTestSuite) TestCommitRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	res, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
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
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationCommitBuildID{
			TargetBuildID: "2.0",
			Force:         false,
		},
	})
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	// Use the `force`...
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationCommitBuildID{
			TargetBuildID: "2.0",
			Force:         true,
		},
	})
	ts.NoError(err)

	res, err = ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
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

	res, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.0",
			},
		},
	})
	ts.NoError(err)

	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken, // duplicated token
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
			},
		},
	})
	ts.Error(err)
	ts.IsType(&serviceerror.FailedPrecondition{}, err)

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken, // original token
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "2.0",
			},
		},
	})
	ts.NoError(err)
}

func (ts *WorkerVersioningTestSuite) TestTwoWorkersGetDifferentTasks() {
	// TODO: Unskip this test, it is flaky with server 1.25.0-rc.0
	if os.Getenv("DISABLE_SERVER_1_25_TESTS") != "" {
		ts.T().SkipNow()
	}
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

	res, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: res.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
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
	_, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
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

func (ts *WorkerVersioningTestSuite) TestReachabilityUnreachableWithRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	buildID := uuid.New()

	taskQueueInfo, err := ts.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
		TaskQueue: ts.taskQueueName,
		Versions: &client.TaskQueueVersionSelection{
			BuildIDs: []string{buildID},
		},
		TaskQueueTypes: []client.TaskQueueType{
			client.TaskQueueTypeWorkflow,
			client.TaskQueueTypeActivity,
			client.TaskQueueTypeNexus,
		},
		ReportPollers:          true,
		ReportTaskReachability: true,
	})
	ts.NoError(err)

	ts.Equal(1, len(taskQueueInfo.VersionsInfo))
	taskQueueVersionInfo, ok := taskQueueInfo.VersionsInfo[buildID]
	ts.True(ok)
	ts.Equal(client.BuildIDTaskReachability(client.BuildIDTaskReachabilityUnreachable), taskQueueVersionInfo.TaskReachability)

	ts.Equal(3, len(taskQueueVersionInfo.TypesInfo))
	taskQueueTypeInfo, ok := taskQueueVersionInfo.TypesInfo[client.TaskQueueTypeWorkflow]
	ts.True(ok)
	ts.Equal(0, len(taskQueueTypeInfo.Pollers))
	taskQueueTypeInfo, ok = taskQueueVersionInfo.TypesInfo[client.TaskQueueTypeActivity]
	ts.True(ok)
	ts.Equal(0, len(taskQueueTypeInfo.Pollers))
	taskQueueTypeInfo, ok = taskQueueVersionInfo.TypesInfo[client.TaskQueueTypeNexus]
	ts.True(ok)
	ts.Equal(0, len(taskQueueTypeInfo.Pollers))
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

func (ts *WorkerVersioningTestSuite) TestReachabilityUnversionedWorkerWithRules() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{Identity: "worker1"})
	ts.workflows.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	// Give time for worker pollers stats to show up
	time.Sleep(2 * time.Second)

	taskQueueInfo, err := ts.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
		TaskQueue: ts.taskQueueName,
		Versions: &client.TaskQueueVersionSelection{
			// `client.UnversionedBuildID` is an empty string
			BuildIDs: []string{client.UnversionedBuildID},
		},
		TaskQueueTypes: []client.TaskQueueType{
			client.TaskQueueTypeWorkflow,
		},
		ReportPollers:          true,
		ReportTaskReachability: true,
	})
	ts.NoError(err)
	ts.Equal(1, len(taskQueueInfo.VersionsInfo))

	taskQueueVersionInfo, ok := taskQueueInfo.VersionsInfo[client.UnversionedBuildID]
	ts.True(ok)
	ts.Equal(client.BuildIDTaskReachability(client.BuildIDTaskReachabilityReachable), taskQueueVersionInfo.TaskReachability)

	ts.Equal(1, len(taskQueueVersionInfo.TypesInfo))
	taskQueueTypeInfo, ok := taskQueueVersionInfo.TypesInfo[client.TaskQueueTypeWorkflow]
	ts.True(ok)
	ts.True(len(taskQueueTypeInfo.Pollers) > 0)
	ts.Equal("worker1", taskQueueTypeInfo.Pollers[0].Identity)
	ts.Equal(false, taskQueueTypeInfo.Pollers[0].WorkerVersionCapabilities.UseVersioning)
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

func (ts *WorkerVersioningTestSuite) TestReachabilityVersionsWithRules() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	buildID1 := uuid.New()
	buildID2 := uuid.New()

	result, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	result, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: result.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: buildID1,
			},
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
	result, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: result.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: buildID2,
			},
		},
	})
	ts.NoError(err)

	time.Sleep(15 * time.Second)

	taskQueueInfo, err := ts.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
		TaskQueue: ts.taskQueueName,
		Versions: &client.TaskQueueVersionSelection{
			BuildIDs: []string{buildID1, buildID2},
		},
		TaskQueueTypes: []client.TaskQueueType{
			client.TaskQueueTypeWorkflow,
		},
		ReportTaskReachability: true,
	})
	ts.NoError(err)
	ts.Equal(2, len(taskQueueInfo.VersionsInfo))

	// Test the first worker
	taskQueueVersionInfo, ok := taskQueueInfo.VersionsInfo[buildID1]
	ts.True(ok)
	ts.Equal(client.BuildIDTaskReachability(client.BuildIDTaskReachabilityClosedWorkflowsOnly), taskQueueVersionInfo.TaskReachability)

	// Test the second worker
	taskQueueVersionInfo, ok = taskQueueInfo.VersionsInfo[buildID2]
	ts.True(ok)
	ts.Equal(client.BuildIDTaskReachability(client.BuildIDTaskReachabilityReachable), taskQueueVersionInfo.TaskReachability)
}

func (ts *WorkerVersioningTestSuite) TestTaskQueueStats() {
	if os.Getenv("DISABLE_BACKLOG_STATS_TESTS") != "" {
		ts.T().SkipNow()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fetchAndValidateStats := func(expectedWorkflowStats *client.TaskQueueStats, expectedActivityStats *client.TaskQueueStats) {
		taskQueueInfo, err := ts.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
			TaskQueue: ts.taskQueueName,
			TaskQueueTypes: []client.TaskQueueType{
				client.TaskQueueTypeWorkflow,
				client.TaskQueueTypeActivity,
			},
			ReportStats: true,
		})
		ts.NoError(err)
		ts.Equal(1, len(taskQueueInfo.VersionsInfo))

		ts.validateTaskQueueStats(expectedWorkflowStats, taskQueueInfo.VersionsInfo[""].TypesInfo[client.TaskQueueTypeWorkflow].Stats)
		ts.validateTaskQueueStats(expectedActivityStats, taskQueueInfo.VersionsInfo[""].TypesInfo[client.TaskQueueTypeActivity].Stats)
	}

	// Basic workflow runs two activities
	handle, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("basic-wf"), ts.workflows.Basic)
	ts.NoError(err)

	// Wait until the task goes to the TQ
	ts.EventuallyWithT(
		func(t *assert.CollectT) {
			taskQueueInfo, err := ts.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
				TaskQueue: ts.taskQueueName,
				TaskQueueTypes: []client.TaskQueueType{
					client.TaskQueueTypeWorkflow,
				},
				ReportStats: true,
			})
			ts.NoError(err)
			ts.Equal(1, len(taskQueueInfo.VersionsInfo))
			ts.NotNil(taskQueueInfo.VersionsInfo[""].TypesInfo[client.TaskQueueTypeWorkflow])
			ts.NotNil(taskQueueInfo.VersionsInfo[""].TypesInfo[client.TaskQueueTypeWorkflow].Stats)
			assert.Greater(t, taskQueueInfo.VersionsInfo[""].TypesInfo[client.TaskQueueTypeWorkflow].Stats.ApproximateBacklogCount, int64(0))
		},
		time.Second, 100*time.Millisecond,
	)

	// no workers yet, so only workflow should have a backlog
	fetchAndValidateStats(
		&client.TaskQueueStats{
			ApproximateBacklogCount: 1,
			ApproximateBacklogAge:   time.Millisecond,
			BacklogIncreaseRate:     1,
			TasksAddRate:            1,
			TasksDispatchRate:       0,
		},
		&client.TaskQueueStats{
			ApproximateBacklogCount: 0,
			ApproximateBacklogAge:   0,
			BacklogIncreaseRate:     0,
			TasksAddRate:            0,
			TasksDispatchRate:       0,
		},
	)

	// run the worker
	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{DisableEagerActivities: true})
	ts.workflows.register(worker1)
	ts.activities.register(worker1)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	// Wait for the wf to finish
	ts.NoError(handle.Get(ctx, nil))

	// backlogs should be empty but the rates should be non-zero
	fetchAndValidateStats(
		&client.TaskQueueStats{
			ApproximateBacklogCount: 0,
			ApproximateBacklogAge:   0,
			BacklogIncreaseRate:     0,
			TasksAddRate:            1,
			TasksDispatchRate:       1,
		},
		&client.TaskQueueStats{
			ApproximateBacklogCount: 0,
			ApproximateBacklogAge:   0,
			BacklogIncreaseRate:     0,
			TasksAddRate:            1,
			TasksDispatchRate:       1,
		},
	)
}

func (ts *WorkerVersioningTestSuite) TestBuildIDChangesOverWorkflowLifetime() {
	// TODO: Unskip this test, it is flaky with server 1.25.0-rc.0
	if os.Getenv("DISABLE_SERVER_1_25_TESTS") != "" {
		ts.T().SkipNow()
	}
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
	// TODO: Unskip this test, it is flaky with server 1.25.0-rc.0
	if os.Getenv("DISABLE_SERVER_1_25_TESTS") != "" {
		ts.T().SkipNow()
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	result, err := ts.client.GetWorkerVersioningRules(ctx, client.GetWorkerVersioningOptions{
		TaskQueue: ts.taskQueueName,
	})
	ts.NoError(err)

	resp, err := ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: result.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
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
	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationInsertAssignmentRule{
			RuleIndex: 0,
			Rule: client.VersioningAssignmentRule{
				TargetBuildID: "1.1",
			},
		},
	})

	resp, err = ts.client.UpdateWorkerVersioningRules(ctx, client.UpdateWorkerVersioningRulesOptions{
		TaskQueue:     ts.taskQueueName,
		ConflictToken: resp.ConflictToken,
		Operation: &client.VersioningOperationAddRedirectRule{
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

// validateTaskQueueStats compares expected vs actual stats.
// For age and rates, it treats all non-zero values the same.
// For BacklogIncreaseRate for non-zero expected values we only compare the sign (i.e. backlog grows or shrinks), while
// zero expected value means "not specified".
func (ts *WorkerVersioningTestSuite) validateTaskQueueStats(expected *client.TaskQueueStats, actual *internal.TaskQueueStats) {
	if expected == nil {
		ts.Nil(actual)
		return
	}
	ts.NotNil(actual)
	ts.Equal(expected.ApproximateBacklogCount, actual.ApproximateBacklogCount)
	if expected.ApproximateBacklogAge == 0 {
		ts.Equal(time.Duration(0), actual.ApproximateBacklogAge)
	} else {
		ts.Greater(actual.ApproximateBacklogAge, time.Duration(0))
	}
	if expected.TasksAddRate == 0 {
		// TODO: do not accept NaN once the server code is fixed: https://github.com/temporalio/temporal/pull/6404
		ts.True(float32(0) == actual.TasksAddRate || math.IsNaN(float64(actual.TasksAddRate)))
	} else {
		ts.Greater(actual.TasksAddRate, float32(0))
	}
	if expected.TasksDispatchRate == 0 {
		// TODO: do not accept NaN once the server code is fixed: https://github.com/temporalio/temporal/pull/6404
		ts.True(float32(0) == actual.TasksDispatchRate || math.IsNaN(float64(actual.TasksDispatchRate)))
	} else {
		ts.Greater(actual.TasksDispatchRate, float32(0))
	}
	if expected.BacklogIncreaseRate > 0 {
		ts.Greater(actual.BacklogIncreaseRate, float32(0))
	} else if expected.BacklogIncreaseRate < 0 {
		ts.Less(actual.BacklogIncreaseRate, float32(0))
	}
}
