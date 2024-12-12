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

func IsVersionOne(result string) bool {
	return strings.HasSuffix(result, "_v1")
}

func IsVersionTwo(result string) bool {
	return strings.HasSuffix(result, "_v2")
}

type DeploymentTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	workflows2 *Workflows
	activities *Activities
}

func TestDeploymentTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentTestSuite))
}

func (ts *DeploymentTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = newActivities()
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *DeploymentTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *DeploymentTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *DeploymentTestSuite) waitForWorkflowRunning(ctx context.Context, handle client.WorkflowRun) {
	ts.Eventually(func() bool {
		describeResp, err := ts.client.DescribeWorkflowExecution(ctx, handle.GetID(), handle.GetRunID())
		ts.NoError(err)
		status := describeResp.WorkflowExecutionInfo.Status
		return enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING == status
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *DeploymentTestSuite) waitForReachability(ctx context.Context, deployment client.Deployment, target client.DeploymentReachability) {
	ts.Eventually(func() bool {
		info, err := ts.client.DeploymentClient().GetReachability(ctx, client.DeploymentGetReachabilityOptions{
			Deployment: deployment,
		})
		ts.NoError(err)

		return info.Reachability == target
	}, 70*time.Second, 1000*time.Millisecond)
}

func (ts *DeploymentTestSuite) TestPinnedBehaviorThreeWorkers() {
	if os.Getenv("DISABLE_DEPLOYMENT_TESTS") != "" {
		ts.T().Skip("temporal server 1.26.2+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	seriesName := "deploy-test-" + uuid.New()

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
		BuildID:                 "1.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "2.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	worker3 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "3.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})

	worker3.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker3.Start())
	defer worker3.Stop()

	_, err := ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "1.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	// start workflow1 with 1.0, WaitSignalToStartVersionedOne, auto-upgrade
	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle1)

	_, err = ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "2.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	// start workflow2 with 2.0, WaitSignalToStartVersionedOne, pinned
	handle2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("2"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle2)

	_, err = ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "3.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	resp, err := ts.client.DeploymentClient().GetCurrent(ctx, client.DeploymentGetCurrentOptions{
		SeriesName: seriesName,
	})
	ts.NoError(err)
	ts.Equal(resp.DeploymentInfo.Deployment.BuildID, "3.0")

	desc, err := ts.client.DeploymentClient().Describe(ctx, client.DeploymentDescribeOptions{
		Deployment: client.Deployment{
			SeriesName: seriesName,
			BuildID:    "3.0",
		},
	})
	ts.NoError(err)
	ts.True(desc.DeploymentInfo.IsCurrent)

	// start workflow3 with 3.0, WaitSignalToStartVersionedTwo, auto-upgrade
	handle3, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("3"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle3)

	// SetCurrent seems to be eventually consistent for auto-update workflows,
	// even though GetCurrent returns the new version.
	// TBD(antlai-temporal) verify with server team whether this is expected.
	time.Sleep(1 * time.Second)

	// finish them all
	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle2.GetID(), handle2.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle3.GetID(), handle3.GetRunID(), "start-signal", "prefix"))

	// Wait for all wfs to finish
	var result string
	ts.NoError(handle1.Get(ctx, &result))
	//Auto-upgraded to 3.0
	ts.True(IsVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// Pinned to 2.0
	ts.True(IsVersionOne(result))

	ts.NoError(handle3.Get(ctx, &result))
	// AutoUpgrade to 3.0
	ts.True(IsVersionTwo(result))
}

func (ts *DeploymentTestSuite) TestPinnedOverrideInWorkflowOptions() {
	if os.Getenv("DISABLE_DEPLOYMENT_TESTS") != "" {
		ts.T().Skip("temporal server 1.26.2+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	seriesName := "deploy-test-" + uuid.New()

	// Two workers:
	// 1) 1.0 with WaitSignalToStartVersionedOne (setCurrent)
	// 2) 2.0 with WaitSignalToStartVersionedTwo
	// Two workflows:
	// 1) started with "2.0" WorkflowOptions to override SetCurrent
	// 2) started with no options to use SetCurrent ("1.0")

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "1.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "2.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	_, err := ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "1.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	// start workflow1 with 2.0, WaitSignalToStartVersionedTwo
	options := ts.startWorkflowOptions("1")
	options.VersioningOverride = client.VersioningOverride{
		Behavior: workflow.VersioningBehaviorPinned,
		Deployment: client.Deployment{
			SeriesName: seriesName,
			BuildID:    "2.0",
		},
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
	ts.True(IsVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// No Override
	ts.True(IsVersionOne(result))
}

func (ts *DeploymentTestSuite) TestUpdateWorkflowExecutionOptions() {
	if os.Getenv("DISABLE_DEPLOYMENT_TESTS") != "" {
		ts.T().Skip("temporal server 1.26.2+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	seriesName := "deploy-test-" + uuid.New()

	// Two workers:
	// 1) 1.0 with WaitSignalToStartVersionedOne (setCurrent)
	// 2) 2.0 with WaitSignalToStartVersionedTwo
	// Three workflows:
	// 1) started with "1.0", manual override to "2.0", finish "2.0"
	// 2) started with "1.0", manual override to "2.0", remove override, finish "1.0"
	// 3) started with "1.0", no override, finishes with "1.0" unaffected

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "1.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})
	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "2.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})

	worker2.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedTwo, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorAutoUpgrade,
	})

	ts.NoError(worker2.Start())
	defer worker2.Stop()

	_, err := ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "1.0",
			SeriesName: seriesName,
		},
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

	options, err := ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle1.GetID(),
		RunId:      handle1.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{
				Behavior: workflow.VersioningBehaviorPinned,
				Deployment: client.Deployment{
					SeriesName: seriesName,
					BuildID:    "2.0",
				},
			},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride.Deployment.BuildID, "2.0")

	// Add and remove override to handle2
	options, err = ts.client.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
		WorkflowId: handle2.GetID(),
		RunId:      handle2.GetRunID(),
		WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
			VersioningOverride: &client.VersioningOverride{
				Behavior: workflow.VersioningBehaviorPinned,
				Deployment: client.Deployment{
					SeriesName: seriesName,
					BuildID:    "2.0",
				},
			},
		},
	})
	ts.NoError(err)
	ts.Equal(options.VersioningOverride.Deployment.BuildID, "2.0")

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

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle2.GetID(), handle2.GetRunID(), "start-signal", "prefix"))
	ts.NoError(ts.client.SignalWorkflow(ctx, handle3.GetID(), handle3.GetRunID(), "start-signal", "prefix"))

	// Wait for all wfs to finish
	var result string
	ts.NoError(handle1.Get(ctx, &result))
	// override
	ts.True(IsVersionTwo(result))

	ts.NoError(handle2.Get(ctx, &result))
	// override deleted
	ts.True(IsVersionOne(result))

	ts.NoError(handle3.Get(ctx, &result))
	// no override
	ts.True(IsVersionOne(result))
}

func (ts *DeploymentTestSuite) TestListDeployments() {
	if os.Getenv("DISABLE_DEPLOYMENT_TESTS") != "" {
		ts.T().Skip("temporal server 1.26.2+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	seriesName1 := "deploy-test-" + uuid.New()
	seriesName2 := "deploy-test-" + uuid.New()

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "1.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName1,
		},
	})
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "2.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName2,
		},
	})
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	worker3 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "3.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName2,
		},
	})
	ts.NoError(worker3.Start())
	defer worker3.Stop()

	ts.Eventually(func() bool {
		iter, err := ts.client.DeploymentClient().List(ctx, client.DeploymentListOptions{
			SeriesName: seriesName2,
			PageSize:   1,
		})
		ts.NoError(err)

		var deployments []*client.DeploymentListEntry
		for iter.HasNext() {
			depl, err := iter.Next()
			if err != nil {
				return false
			}
			deployments = append(deployments, depl)
		}

		res := []string{}
		for _, depl := range deployments {
			if depl.IsCurrent {
				return false
			}
			res = append(res, depl.Deployment.BuildID+depl.Deployment.SeriesName)
		}
		sort.Strings(res)
		return reflect.DeepEqual(res, []string{"2.0" + seriesName2, "3.0" + seriesName2})
	}, 10*time.Second, 300*time.Millisecond)

}

func (ts *DeploymentTestSuite) TestDeploymentReachability() {
	if os.Getenv("DISABLE_DEPLOYMENT_TESTS") != "" {
		ts.T().Skip("temporal server 1.26.2+ required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	seriesName := "deploy-test-" + uuid.New()

	worker1 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "1.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
		},
	})
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker1.RegisterWorkflowWithOptions(ts.workflows.WaitSignalToStartVersionedOne, workflow.RegisterOptions{
		Name:               "WaitSignalToStartVersioned",
		VersioningBehavior: workflow.VersioningBehaviorPinned,
	})

	worker2 := worker.New(ts.client, ts.taskQueueName, worker.Options{
		BuildID:                 "2.0",
		UseBuildIDForVersioning: true,
		DeploymentOptions: worker.DeploymentOptions{
			DeploymentSeriesName: seriesName,
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

	_, err := ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "1.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("1"), "WaitSignalToStartVersioned")
	ts.NoError(err)

	ts.waitForWorkflowRunning(ctx, handle1)

	ts.waitForReachability(ctx, client.Deployment{
		SeriesName: seriesName,
		BuildID:    "1.0",
	}, client.DeploymentReachabilityReachable)

	ts.waitForReachability(ctx, client.Deployment{
		SeriesName: seriesName,
		BuildID:    "2.0",
	}, client.DeploymentReachabilityUnreachable)

	_, err = ts.client.DeploymentClient().SetCurrent(ctx, client.DeploymentSetCurrentOptions{
		Deployment: client.Deployment{
			BuildID:    "2.0",
			SeriesName: seriesName,
		},
	})
	ts.NoError(err)

	// SetCurrent seems to be eventually consistent for auto-update workflows,
	// even though GetCurrent returns the new version.
	// TBD(antlai-temporal) verify with server team whether this is expected.
	time.Sleep(1 * time.Second)

	// Still a workflow executing
	ts.waitForReachability(ctx, client.Deployment{
		SeriesName: seriesName,
		BuildID:    "1.0",
	}, client.DeploymentReachabilityReachable)

	// For new workflows
	ts.waitForReachability(ctx, client.Deployment{
		SeriesName: seriesName,
		BuildID:    "2.0",
	}, client.DeploymentReachabilityReachable)

	ts.NoError(ts.client.SignalWorkflow(ctx, handle1.GetID(), handle1.GetRunID(), "start-signal", "prefix"))

	var result string
	ts.NoError(handle1.Get(ctx, &result))
	// was Pinned
	ts.True(IsVersionOne(result))

	// This test eventually passes but it takes about 60 seconds.
	// TODO(antlai-temporal): Re-enable after speeding up reachability cache refresh.
	//
	// No workflow executing
	//ts.waitForReachability(ctx, client.Deployment{
	//	SeriesName: seriesName,
	//	BuildID:    "1.0",
	//}, client.DeploymentReachabilityClosedWorkflows)

	// For new workflows
	ts.waitForReachability(ctx, client.Deployment{
		SeriesName: seriesName,
		BuildID:    "2.0",
	}, client.DeploymentReachabilityReachable)
}
