package test_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type DynamicWorkflowTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	workflows  *Workflows
	activities *Activities
}

func TestDynamicWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DynamicWorkflowTestSuite))
}

func (ts *DynamicWorkflowTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.workflows = &Workflows{}
	ts.activities = newActivities()
	ts.NoError(ts.InitConfigAndNamespace())
	ts.NoError(ts.InitClient())
}

func (ts *DynamicWorkflowTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
	ts.client.Close()
}

func (ts *DynamicWorkflowTestSuite) SetupTest() {
	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func DynamicWorkflow(ctx workflow.Context, args converter.EncodedValues) (string, error) {
	var result string
	info := workflow.GetInfo(ctx)

	var arg1, arg2 string
	err := args.Get(&arg1, &arg2)
	if err != nil {
		return "", fmt.Errorf("failed to decode arguments: %w", err)
	}

	if info.WorkflowType.Name == "dynamic-activity" {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
		err := workflow.ExecuteActivity(ctx, "random-activity-name", arg1, arg2).Get(ctx, &result)
		if err != nil {
			return "", err
		}
	} else {
		result = fmt.Sprintf("%s - %s - %s", info.WorkflowType.Name, arg1, arg2)
	}

	return result, nil
}

func DynamicActivity(ctx context.Context, args converter.EncodedValues) (string, error) {
	var arg1, arg2 string
	err := args.Get(&arg1, &arg2)
	if err != nil {
		return "", fmt.Errorf("failed to decode arguments: %w", err)
	}

	info := activity.GetInfo(ctx)
	result := fmt.Sprintf("%s - %s - %s", info.WorkflowType.Name, arg1, arg2)

	return result, nil
}

func (ts *DynamicWorkflowTestSuite) TestBasicDynamicWorkflowActivity() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	w := worker.New(ts.client, ts.taskQueueName, worker.Options{})
	w.RegisterDynamicWorkflow(DynamicWorkflow, workflow.DynamicRegisterOptions{})
	w.RegisterDynamicActivity(DynamicActivity, activity.DynamicRegisterOptions{})

	err := w.Start()
	ts.NoError(err)
	defer w.Stop()

	handle, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi"), "some-workflow", "apple", "pear")
	ts.NoError(err)
	var result string
	err = handle.Get(ctx, &result)
	ts.NoError(err)
	ts.Equal("some-workflow - apple - pear", result)

	handle, err = ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi1"), "dynamic-activity", "grape", "cherry")
	ts.NoError(err)
	err = handle.Get(ctx, &result)
	ts.NoError(err)
	ts.Equal("dynamic-activity - grape - cherry", result)
}

func (ts *DynamicWorkflowTestSuite) waitForWorkerDeployment(ctx context.Context, dHandle client.WorkerDeploymentHandle) {
	ts.Eventually(func() bool {
		_, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
		return err == nil
	}, 10*time.Second, 300*time.Millisecond)
}

func (ts *DynamicWorkflowTestSuite) waitForWorkerDeploymentVersion(
	ctx context.Context,
	dHandle client.WorkerDeploymentHandle,
	version worker.WorkerDeploymentVersion,
) {
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

func EmptyDynamic(ctx workflow.Context, args converter.EncodedValues) error {
	return nil
}

func (ts *DynamicWorkflowTestSuite) getVersionFromHistory(ctx context.Context, handle client.WorkflowRun) (enumspb.VersioningBehavior, error) {
	iter := ts.client.GetWorkflowHistory(ctx, handle.GetID(), handle.GetRunID(), true, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		ts.NoError(err)
		if event.GetWorkflowTaskCompletedEventAttributes() != nil {
			return event.GetWorkflowTaskCompletedEventAttributes().VersioningBehavior, nil
		}
	}
	return enumspb.VERSIONING_BEHAVIOR_UNSPECIFIED, errors.New("versioning behavior not found")
}

func (ts *DynamicWorkflowTestSuite) TestBasicDynamicWorkflowActivityWithVersioning() {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
	defer cancel()

	deploymentName := "deploy-test-" + uuid.NewString()
	v1 := worker.WorkerDeploymentVersion{
		DeploymentName: deploymentName,
		BuildId:        "1.0",
	}
	w := worker.New(ts.client, ts.taskQueueName, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version:       v1,
		},
	})
	w.RegisterDynamicWorkflow(EmptyDynamic, workflow.DynamicRegisterOptions{
		LoadDynamicRuntimeOptions: func(details workflow.LoadDynamicRuntimeOptionsDetails) (workflow.DynamicRuntimeOptions, error) {
			var options workflow.DynamicRuntimeOptions
			switch details.WorkflowType.Name {
			case "some-workflow":
				options.VersioningBehavior = workflow.VersioningBehaviorAutoUpgrade
			case "behavior-pinned":
				options.VersioningBehavior = workflow.VersioningBehaviorPinned
			default:
				options.VersioningBehavior = workflow.VersioningBehaviorUnspecified
			}
			return options, nil
		},
	})

	err := w.Start()
	ts.NoError(err)
	defer w.Stop()

	dHandle := ts.client.WorkerDeploymentClient().GetHandle(deploymentName)

	ts.waitForWorkerDeployment(ctx, dHandle)

	response1, err := dHandle.Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	ts.NoError(err)

	ts.waitForWorkerDeploymentVersion(ctx, dHandle, v1)

	_, err = dHandle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		BuildID:       v1.BuildId,
		ConflictToken: response1.ConflictToken,
	})
	ts.NoError(err)

	var result string
	handle1, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi"), "some-workflow")
	ts.NoError(err)

	err = handle1.Get(ctx, &result)
	ts.NoError(err)

	handle2, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi"), "behavior-pinned")
	ts.NoError(err)

	err = handle2.Get(ctx, &result)
	ts.NoError(err)
	handle3, err := ts.client.ExecuteWorkflow(ctx, ts.startWorkflowOptions("hi"), "random-name")
	ts.NoError(err)

	err = handle3.Get(ctx, &result)
	ts.NoError(err)

	var version enumspb.VersioningBehavior
	version, err = ts.getVersionFromHistory(ctx, handle1)
	ts.NoError(err)
	ts.Equal(enumspb.VERSIONING_BEHAVIOR_AUTO_UPGRADE, version)
	version, err = ts.getVersionFromHistory(ctx, handle2)
	ts.NoError(err)
	ts.Equal(enumspb.VERSIONING_BEHAVIOR_PINNED, version)
	version, err = ts.getVersionFromHistory(ctx, handle3)
	ts.NoError(err)
	ts.Equal(enumspb.VERSIONING_BEHAVIOR_UNSPECIFIED, version)
}
