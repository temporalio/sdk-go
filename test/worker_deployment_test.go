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
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pborman/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func IsWorkerVersionOne(result string) bool {
	return strings.HasSuffix(result, "_v1")
}

func IsWorkerVersionTwo(result string) bool {
	return strings.HasSuffix(result, "_v2")
}

type WorkerDeploymentTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	workflows2 *Workflows
	activities *Activities
}

func TestWorkerDeploymentTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerDeploymentTestSuite))
}

func (ts *WorkerDeploymentTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = newActivities()
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *WorkerDeploymentTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *WorkerDeploymentTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerDeploymentTestSuite) waitForWorkerDeployment(ctx context.Context, dHandle client.WorkerDeploymentHandle) {
	ts.Eventually(func() bool {
		_, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
		return err == nil
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) waitForWorkerDeploymentVersion(ctx context.Context, dHandle client.WorkerDeploymentHandle, version string) {
	ts.Eventually(func() bool {
		d, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
		if err != nil {
			return false
		}
		for _, v := range d.Info.VersionSummaries {
			if v.Version == version {
				return true
			}
		}
		return false
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) waitForWorkflowRunning(ctx context.Context, handle client.WorkflowRun) {
	ts.Eventually(func() bool {
		describeResp, err := ts.client.DescribeWorkflowExecution(ctx, handle.GetID(), handle.GetRunID())
		ts.NoError(err)
		status := describeResp.WorkflowExecutionInfo.Status
		return enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING == status
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) waitForDrainage(ctx context.Context, dHandle client.WorkerDeploymentHandle, version string, target client.WorkerDeploymentVersionDrainageStatus) {
	ts.Eventually(func() bool {
		desc, err := dHandle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
			Version: version,
		})
		return err == nil && desc.Info.DrainageInfo != nil &&
			desc.Info.DrainageInfo.DrainageStatus == target
	}, 181*time.Second, 1000*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) runWorkflowAndCheckV1(ctx context.Context, wfID string) bool {
	// start workflow1 with 1.0, WaitSignalToStartVersionedOne, auto-upgrade
	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions(wfID), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	// Wait for all wfs to finish
	var result string
	ts.NoError(handle1.Get(ctx, &result))

	return IsWorkerVersionOne(result)
}

func (ts *WorkerDeploymentTestSuite) TestPinnedBehaviorThreeWorkers() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	// Start three workers:
	// 1.0) AutoUpgrade, WaitSignalToStartVersionedOne
	// 2.0) Pinned, WaitSignalToStartVersionedOne
	// 3.0) Pinned (does not matter), WaitSignalToStartVersionedTwo
	//
	// Start three workflows:
	// 1) Should be AutoUpgrade, starts with WaitSignalToStartVersionedOne (1.0),
	//     and ends with WaitSignalToStartVersionedTwo (3.0)
	// 2) Should be pinned, starts with WaitSignalToStartVersionedOne (2.0),
	//     and ends with WaitSignalToStartVersionedOne (2.0)
	// 3) should be AutoUpgrade, starts/ends with WaitSignalToStartVersionedTwo (3.0)

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".2.0"),
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	worker3 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".3.0"),
		},
	})

	worker3.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker3.Start())
	defer worker3.Stop()
	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".1.0")

	response2, err := dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".1.0",
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	// start workflow1 with 1.0, WaitSignalToStartVersionedOne, auto-upgrade
	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle1)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".2.0")

	response3, err := dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response2.ConflictToken,
	})
	ts.NoError(err)

	// start workflow2 with 2.0, WaitSignalToStartVersionedOne, pinned
	handle2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle2)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".3.0")

	// Needed if server constant maxFastUserDataFetches is not >= 20
	//time.Sleep(10 * time.Second)

	_, err = dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".3.0",
		ConflictToken: response3.ConflictToken,
		Identity:      "client1",
	})
	ts.NoError(err)

	desc, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})

	ts.NoError(err)
	ts.Equal(deploymentName, desc.Info.Name)

	ts.Equal("client1", desc.Info.LastModifierIdentity)
	ts.Equal(deploymentName+".3.0", desc.Info.RoutingConfig.CurrentVersion)
	ts.Equal("", desc.Info.RoutingConfig.RampingVersion)
	ts.Equal(float32(0.0), desc.Info.RoutingConfig.RampingVersionPercentage)
	ts.Equal(3, len(desc.Info.VersionSummaries))
	sort.Slice(desc.Info.VersionSummaries, func(i, j int) bool {
		return desc.Info.VersionSummaries[i].Version < desc.Info.VersionSummaries[j].Version
	})
	ts.Equal(deploymentName+".1.0", desc.Info.VersionSummaries[0].Version)
	ts.Equal(client.WorkerDeploymentVersionDrainageStatus(client.WorkerDeploymentVersionDrainageStatusDraining), desc.Info.VersionSummaries[0].DrainageStatus)
	ts.Equal(deploymentName+".2.0", desc.Info.VersionSummaries[1].Version)
	ts.Equal(client.WorkerDeploymentVersionDrainageStatus(client.WorkerDeploymentVersionDrainageStatusDraining), desc.Info.VersionSummaries[0].DrainageStatus)
	ts.Equal(deploymentName+".3.0", desc.Info.VersionSummaries[2].Version)
	// current/ramping shows as unspecified
	ts.Equal(client.WorkerDeploymentVersionDrainageStatus(client.WorkerDeploymentVersionDrainageStatusUnspecified), desc.Info.VersionSummaries[2].DrainageStatus)

	// start workflow3 with 3.0, WaitSignalToStartVersionedTwo, auto-upgrade
	handle3, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("3"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle3)

	// finish them all
	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle2.GetID(), handle2.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle3.GetID(), handle3.GetRunID(), "start-signal", "prefix"))

	// Wait for all wfs to finish
	var result string
	ts.NoError(handle1.Get(ctx, &result))
	//Auto-upgraded to 3.0
	ts.True(IsWorkerVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// Pinned to 2.0
	ts.True(IsWorkerVersionOne(result))

	ts.NoError(handle3.Get(ctx, &result))
	// AutoUpgrade to 3.0
	ts.True(IsWorkerVersionTwo(result))
}

func (ts *WorkerDeploymentTestSuite) TestPinnedOverrideInWorkflowOptions() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	// Two workers:
	// 1) 1.0 with WaitSignalToStartVersionedOne (setCurrent)
	// 2) 2.0 with WaitSignalToStartVersionedTwo
	// Two workflows:
	// 1) started with "2.0" WorkflowOptions to override SetCurrent
	// 2) started with no options to use SetCurrent ("1.0")

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.DeploymentVersion{DeploymentName: deploymentName, BuildID: "2.0"},
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".1.0")

	_, err = dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".1.0",
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	// start workflow1 with 2.0, WaitSignalToStartVersionedTwo
	options := ts.startWorkflowOptions("1")
	options.VersioningOverride = client.VersioningOverride{
		Behavior:      workflow.VersioningBehaviorPinned,
		PinnedVersion: deploymentName + ".2.0",
	}
	handle1, err := ts.client.ExecuteWorkflow(ctx, options, "WaitSignalToStartVersioned")
	ts.NoError(err)
	// No override
	handle2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle2.GetID(), handle2.GetRunID(), "start-signal", "prefix"))

	var result string
	ts.NoError(handle1.Get(ctx, &result))
	// Override with WorkflowOptions
	ts.True(IsWorkerVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// No Override
	ts.True(IsWorkerVersionOne(result))
}

func (ts *WorkerDeploymentTestSuite) TestUpdateWorkflowExecutionOptions() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	// Two workers:
	// 1) 1.0 with WaitSignalToStartVersionedOne (setCurrent)
	// 2) 2.0 with WaitSignalToStartVersionedTwo
	// Four workflows:
	// 1) started with "1.0", manual override to "2.0", finish "2.0"
	// 2) started with "1.0", manual override to "2.0", remove override, finish "1.0"
	// 3) started with "1.0", no override, finishes with "1.0" unaffected
	// 4) started with "1.0", manual override to auto-upgrade, finishes with "2.0"

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".2.0"),
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()
	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".1.0")

	response2, err := dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".1.0",
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle1)

	handle2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle2)

	handle3, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("3"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	handle4, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("4"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle4)

	options, err := ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle1.GetID(),
		RunId:      handle1.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{
				Behavior:      workflow.VersioningBehaviorPinned,
				PinnedVersion: deploymentName + ".2.0",
			},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride.PinnedVersion, deploymentName+".2.0")

	// Add and remove override to handle2
	options, err = ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle2.GetID(),
		RunId:      handle2.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{
				Behavior:      workflow.VersioningBehaviorPinned,
				PinnedVersion: deploymentName + ".2.0",
			},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride.PinnedVersion, deploymentName+".2.0")

	// Now delete it
	options, err = ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle2.GetID(),
		RunId:      handle2.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride, client.VersioningOverride{})

	// Add autoUpgrade to handle4
	options, err = ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle4.GetID(),
		RunId:      handle4.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{
				Behavior: workflow.VersioningBehaviorAutoUpgrade,
			},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride.PinnedVersion, "")
	ts.Equal(options.VersioningOverride.Behavior, workflow.VersioningBehaviorAutoUpgrade)

	_, err = dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response2.ConflictToken,
	})
	ts.NoError(err)

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle2.GetID(), handle2.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle3.GetID(), handle3.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle4.GetID(), handle4.GetRunID(), "start-signal", "prefix"))

	// Wait for all wfs to finish
	var result string
	ts.NoError(handle1.Get(ctx, &result))
	// override
	ts.True(IsWorkerVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// override deleted
	ts.True(IsWorkerVersionOne(result))

	ts.NoError(handle3.Get(ctx, &result))
	// no override
	ts.True(IsWorkerVersionOne(result))

	ts.NoError(handle4.Get(ctx, &result))
	// override + autoUpgrade
	ts.True(IsWorkerVersionTwo(result))
}

func (ts *WorkerDeploymentTestSuite) TestListDeployments() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	uuid := uuid.New()
	deploymentName1 := uuid + "-deploy-test1"
	deploymentName2 := uuid + "-deploy-test2"

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName1 + ".1.0"),
		},
	})
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName2 + ".2.0"),
		},
	})
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	worker3 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName2 + ".3.0"),
		},
	})
	ts.NoError(worker3.Start())
	defer worker3.Stop()

	ts.Eventually(func() bool {
		iter, err := ts.client.WorkerDeploymentClient().List(ctx, client.WorkerDeploymentListOptions{
			PageSize: 1,
		})
		ts.NoError(err)

		var deployments []*client.WorkerDeploymentListEntry
		for iter.HasNext() {
			depl, err := iter.Next()
			if err != nil {
				return false
			}
			if strings.HasPrefix(depl.Name, uuid) {
				deployments = append(deployments, depl)
			}
		}

		res := []string{}
		for _, depl := range deployments {
			if depl.RoutingConfig.CurrentVersion != "__unversioned__" {
				return false
			}
			res = append(res, depl.Name)
		}
		sort.Strings(res)
		return reflect.DeepEqual(res, []string{deploymentName1, deploymentName2})
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) TestDeploymentDrainage() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	// default VersionDrainageStatusVisibilityGracePeriod is 180 seconds
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	// Start two workers:
	// 1.0) Pinned and 2.0) AutoUpgrade
	//
	// SetCurrent to 1.0) show no drainage in 1.0) and 2.0)
	// Start workflow on 1.0)
	// SetCurrent to 2.0) show 1.0) draining and 2.0) not draining
	// Signal workflow to complete, show 1.0) drained

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".2.0"),
		},
	})
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// SetCurrent to 1.0

	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".1.0")

	response2, err := dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".1.0",
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	// Show no drainage

	desc, err := dHandle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
		Version: deploymentName + ".1.0",
	})
	ts.NoError(err)
	// Current
	ts.Nil(desc.Info.DrainageInfo)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".2.0")
	desc, err = dHandle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
		Version: deploymentName + ".2.0",
	})
	ts.NoError(err)
	// No workflows started
	ts.Nil(desc.Info.DrainageInfo)

	// Start workflow on 1.0)

	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle1)

	// SetCurrent to 2.0)
	_, err = dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response2.ConflictToken,
	})
	ts.NoError(err)

	// Show 1.0) Draining and 2.0) not

	ts.waitForDrainage(ctx, dHandle, deploymentName+".1.0", client.WorkerDeploymentVersionDrainageStatusDraining)

	desc, err = dHandle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
		Version: deploymentName + ".2.0",
	})
	ts.NoError(err)
	// Current
	ts.Nil(desc.Info.DrainageInfo)

	// Signal workflow to completion

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))

	var result string
	ts.NoError(handle1.Get(ctx, &result))
	// was Pinned
	ts.True(IsWorkerVersionOne(result))

	resp, err := ts.client.DescribeWorkflowExecution(ctx, handle1.GetID(), handle1.GetRunID())
	ts.NoError(err)
	ts.Equal(enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, resp.GetWorkflowExecutionInfo().GetStatus())

	// 1.0) Drained 2.0) current/no drainage

	ts.waitForDrainage(ctx, dHandle, deploymentName+".1.0", client.WorkerDeploymentVersionDrainageStatusDrained)

	desc, err = dHandle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
		Version: deploymentName + ".2.0",
	})
	ts.NoError(err)
	// Current
	ts.Nil(desc.Info.DrainageInfo)
}

func (ts *WorkerDeploymentTestSuite) TestRampVersions() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	// Two workers:
	// 1.0) and 2.0) both pinned by default
	// SetCurrent to 1.0
	// Ramp 100% to 2.0)
	// Two workflows:
	//  Verify they end in 2.0)
	// Ramp  0% to 2.0)
	// Two workflows:
	//  Verify they end in 1.0)
	// Ramp 50% to 2.0
	// Repeat workflow until one ends in 2.0

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".2.0"),
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".1.0")

	response2, err := dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		Version:       deploymentName + ".1.0",
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, deploymentName+".2.0")

	// Ramp 100% to 2.0

	response3, err := dHandle.SetRampingVersion(ctx, client.WorkerDeploymentSetRampingVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response2.ConflictToken,
		Percentage:    float32(100.0),
	})
	ts.NoError(err)

	ts.True(!ts.runWorkflowAndCheckV1(ctx, "1"))
	ts.True(!ts.runWorkflowAndCheckV1(ctx, "2"))

	// Ramp 0% to 2.0
	response4, err := dHandle.SetRampingVersion(ctx, client.WorkerDeploymentSetRampingVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response3.ConflictToken,
		Percentage:    float32(0.0),
	})
	ts.NoError(err)

	ts.True(ts.runWorkflowAndCheckV1(ctx, "1"))
	ts.True(ts.runWorkflowAndCheckV1(ctx, "2"))

	// Ramp 0% to 2.0
	_, err = dHandle.SetRampingVersion(ctx, client.WorkerDeploymentSetRampingVersionOptions{
		Version:       deploymentName + ".2.0",
		ConflictToken: response4.ConflictToken,
		Percentage:    float32(50.0),
	})
	ts.NoError(err)

	// very likely probability (1-2^33) of success
	ts.Eventually(func() bool {
		return !ts.runWorkflowAndCheckV1(ctx, uuid.New())
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *WorkerDeploymentTestSuite) TestDeleteDeployment() {
	if os.Getenv("DISABLE_SERVER_1_27_TESTS") != "" {
		ts.T().Skip("temporal server 1.27+ required")
	}
	// TODO(antlai-temporal): find ways to speed up deletion
	ts.T().Skip("Taking over 5 min to detect no active pollers in server v1.27.0-128.4")
	ctx, cancel := context.WithTimeout(context.Background(), 310*time.Second)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.New()

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       worker.NewDeploymentVersionFromString(deploymentName + ".1.0"),
		},
	})

	ts.NoError(worker1.Start())

	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	// No pollers
	worker1.Stop()
	ts.client.Close()

	client2, err := ts.newClient()
	ts.NoError(err)
	ts.client = client2

	dHandle = ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	// Delete version
	ts.Eventually(func() bool {
		_, err := dHandle.DeleteVersion(ctx, client.WorkerDeploymentDeleteVersionOptions{
			Version:      deploymentName + ".1.0",
			SkipDrainage: true,
		})
		if err != nil {
			return false
		}
		resp, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
		ts.NoError(err)
		return len(resp.Info.VersionSummaries) == 0
	}, 305*time.Second, 1000*time.Millisecond)

	// Delete deployment with no versions
	_, err = ts.client.WorkerDeploymentClient().Delete(ctx, client.WorkerDeploymentDeleteOptions{
		Name: deploymentName,
	})
	ts.NoError(err)

	ts.Eventually(func() bool {
		iter, err := ts.client.WorkerDeploymentClient().List(ctx, client.WorkerDeploymentListOptions{})
		ts.NoError(err)

		for iter.HasNext() {
			depl, err := iter.Next()
			if err != nil {
				return false
			}
			if depl.Name == deploymentName {
				return false
			}
		}
		return true
	}, 305*time.Second, 1000*time.Millisecond)
}
