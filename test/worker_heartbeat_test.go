package test_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/enums/v1"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/sysinfo"
	"go.temporal.io/sdk/internal"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type WorkerHeartbeatTestSuite struct {
	*require.Assertions
	suite.Suite
	ConfigAndClientSuiteBase
	worker worker.Worker
}

func TestWorkerHeartbeatSuite(t *testing.T) {
	suite.Run(t, new(WorkerHeartbeatTestSuite))
}

func (ts *WorkerHeartbeatTestSuite) SetupSuite() {
	ts.Assertions = require.New(ts.T())
	ts.NoError(ts.InitConfigAndNamespace())
}

func (ts *WorkerHeartbeatTestSuite) TearDownSuite() {
	ts.Assertions = require.New(ts.T())
}

func (ts *WorkerHeartbeatTestSuite) SetupTest() {
	var err error
	heartbeatInterval := 1 * time.Second

	// Create a client with heartbeating enabled
	ts.client, err = client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: &heartbeatInterval,
		ConnectionOptions:       client.ConnectionOptions{TLS: ts.config.TLS},
		Identity:                "WorkerHeartbeatTest",
	})
	ts.NoError(err)

	ts.taskQueueName = taskQueuePrefix + "-" + ts.T().Name()
}

func (ts *WorkerHeartbeatTestSuite) TearDownTest() {
	if ts.worker != nil {
		ts.worker.Stop()
		ts.worker = nil
	}
	if ts.client != nil {
		ts.client.Close()
		ts.client = nil
	}
}

// assertRecentTimestamp asserts the timestamp is within maxAge of now
func (ts *WorkerHeartbeatTestSuite) assertRecentTimestamp(timestamp *timestamppb.Timestamp, maxAge time.Duration, name string) {
	ts.NotNil(timestamp, "%s should not be nil", name)
	ts.False(timestamp.AsTime().IsZero(), "%s should not be zero", name)
	ts.WithinDuration(time.Now(), timestamp.AsTime(), maxAge, "%s should be recent", name)
}

// TestWorkerHeartbeat verifies that worker heartbeats are sent to the server
// and can be queried via ListWorkers and DescribeWorker APIs
func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatBasic() {
	workerStartTime := time.Now()

	worker.SetStickyWorkflowCacheSize(5)
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		MaxConcurrentWorkflowTaskExecutionSize: 5,
		MaxConcurrentActivityExecutionSize:     5,
		DisableEagerActivities:                 true,
	})
	ts.worker.RegisterWorkflow(workflowWithBlockingActivity)
	ts.worker.RegisterActivity(blockingActivity)
	// Register a nexus service so the nexus worker is created and slot info is populated
	nexusService := nexus.NewService("test-heartbeat")
	ts.NoError(nexusService.Register(noopNexusOp))
	ts.worker.RegisterNexusService(nexusService)
	ts.Nil(ts.worker.Start())

	ctx := context.Background()
	wfOptions := ts.startWorkflowOptions("test-worker-heartbeat")

	run, err := ts.client.ExecuteWorkflow(ctx, wfOptions, workflowWithBlockingActivity)
	ts.NoError(err)
	ts.NotNil(run)
	// Wait for activity to start
	select {
	case <-blockingActivityStarted:
		ts.T().Log("Activity started")
	case <-time.After(5 * time.Second):
		ts.Fail("Timeout waiting for activity to start")
	}

	// Wait for heartbeat to capture the in-flight activity
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.ActivityTaskSlotsInfo != nil &&
			workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should find worker with activity slot used")

	ts.Equal(enums.WORKER_STATUS_RUNNING, workerInfo.Status)

	workflowTaskSlots := workerInfo.WorkflowTaskSlotsInfo
	ts.Equal(int32(1), workflowTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(5), workflowTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), workflowTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", workflowTaskSlots.SlotSupplierKind)
	activityTaskSlots := workerInfo.ActivityTaskSlotsInfo
	ts.Equal(int32(0), activityTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(4), activityTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(1), activityTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", activityTaskSlots.SlotSupplierKind)
	nexusTaskSlots := workerInfo.NexusTaskSlotsInfo
	ts.NotNil(nexusTaskSlots)
	ts.Equal(int32(0), nexusTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(1000), nexusTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), nexusTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", nexusTaskSlots.SlotSupplierKind)
	localActivityTaskSlots := workerInfo.LocalActivitySlotsInfo
	ts.Equal(int32(0), localActivityTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(1000), localActivityTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), localActivityTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", localActivityTaskSlots.SlotSupplierKind)

	workflowPollerInfo := workerInfo.WorkflowPollerInfo
	ts.Equal(int32(1), workflowPollerInfo.CurrentPollers)
	stickyPollerInfo := workerInfo.WorkflowStickyPollerInfo
	ts.NotEqual(int32(0), stickyPollerInfo.CurrentPollers)
	nexusPollerInfo := workerInfo.NexusPollerInfo
	ts.Equal(int32(2), nexusPollerInfo.CurrentPollers)
	activityPollerInfo := workerInfo.ActivityPollerInfo
	ts.NotEqual(int32(0), activityPollerInfo.CurrentPollers)

	ts.Equal(int32(1), workerInfo.CurrentStickyCacheSize)

	ts.assertRecentTimestamp(workerInfo.StartTime, 10*time.Second, "StartTime")
	ts.assertRecentTimestamp(workerInfo.HeartbeatTime, 5*time.Second, "HeartbeatTime")

	ts.WithinDuration(workerStartTime, workerInfo.StartTime.AsTime(), 5*time.Second,
		"StartTime should match worker creation time")

	ts.True(workerInfo.HeartbeatTime.AsTime().After(workerInfo.StartTime.AsTime()) ||
		workerInfo.HeartbeatTime.AsTime().Equal(workerInfo.StartTime.AsTime()),
		"HeartbeatTime should be >= StartTime")

	ts.NotNil(workerInfo.ElapsedSinceLastHeartbeat)
	elapsed := workerInfo.ElapsedSinceLastHeartbeat.AsDuration()
	ts.True(elapsed <= 5*time.Second,
		"ElapsedSinceLastHeartbeat should be <= 5s (got %v)", elapsed)

	ts.assertRecentTimestamp(workerInfo.WorkflowPollerInfo.LastSuccessfulPollTime, 5*time.Second,
		"WorkflowPollerInfo.LastSuccessfulPollTime")
	ts.assertRecentTimestamp(workerInfo.ActivityPollerInfo.LastSuccessfulPollTime, 5*time.Second,
		"ActivityPollerInfo.LastSuccessfulPollTime")

	// Store values to compare after shutdown
	firstStartTime := workerInfo.StartTime.AsTime()
	firstHeartbeatTime := workerInfo.HeartbeatTime.AsTime()

	// Signal activity to complete
	blockingActivityComplete <- struct{}{}

	ts.NoError(run.Get(ctx, nil))
	ts.worker.Stop()

	workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
	ts.NotNil(workerInfo, "Should find worker in ListWorkers/DescribeWorker")

	// After shutdown checks
	ts.Equal("WorkerHeartbeatTest", workerInfo.WorkerIdentity)
	hostInfo := workerInfo.HostInfo
	ts.NotEqual("", hostInfo.HostName)
	ts.NotEqual("", hostInfo.ProcessId)
	ts.NotEqual("", hostInfo.WorkerGroupingKey)

	ts.GreaterOrEqual(hostInfo.CurrentHostCpuUsage, float32(0.0))
	ts.GreaterOrEqual(hostInfo.CurrentHostMemUsage, float32(0.0))

	ts.Equal(ts.taskQueueName, workerInfo.TaskQueue)
	ts.Equal(internal.SDKName, workerInfo.SdkName)
	ts.Equal(internal.SDKVersion, workerInfo.SdkVersion)
	ts.Equal(enums.WORKER_STATUS_SHUTTING_DOWN, workerInfo.Status)

	// Timestamp validations - second heartbeat check (after shutdown)
	// StartTime should be unchanged
	ts.Equal(firstStartTime, workerInfo.StartTime.AsTime())

	// HeartbeatTime should have advanced
	ts.True(workerInfo.HeartbeatTime.AsTime().After(firstHeartbeatTime))

	workflowTaskSlots = workerInfo.WorkflowTaskSlotsInfo
	ts.Equal(int32(2), workflowTaskSlots.TotalProcessedTasks)
	ts.Equal("Fixed", workflowTaskSlots.SlotSupplierKind)
	activityTaskSlots = workerInfo.ActivityTaskSlotsInfo
	ts.Equal(int32(1), activityTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(5), activityTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), activityTaskSlots.CurrentUsedSlots)
	ts.Equal(int32(1), activityTaskSlots.LastIntervalProcessedTasks)
	ts.Equal("Fixed", activityTaskSlots.SlotSupplierKind)
	nexusTaskSlots = workerInfo.NexusTaskSlotsInfo
	ts.NotNil(nexusTaskSlots)
	ts.Equal(int32(0), nexusTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(1000), nexusTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), nexusTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", nexusTaskSlots.SlotSupplierKind)
	localActivityTaskSlots = workerInfo.LocalActivitySlotsInfo
	ts.Equal(int32(0), localActivityTaskSlots.TotalProcessedTasks)
	ts.Equal(int32(1000), localActivityTaskSlots.CurrentAvailableSlots)
	ts.Equal(int32(0), localActivityTaskSlots.CurrentUsedSlots)
	ts.Equal("Fixed", localActivityTaskSlots.SlotSupplierKind)

	workflowPollerInfo = workerInfo.WorkflowPollerInfo
	ts.Equal(int32(1), workflowPollerInfo.CurrentPollers)
	ts.False(workflowPollerInfo.IsAutoscaling)
	ts.assertRecentTimestamp(workflowPollerInfo.LastSuccessfulPollTime, 10*time.Second,
		"WorkflowPollerInfo.LastSuccessfulPollTime after shutdown")

	stickyPollerInfo = workerInfo.WorkflowStickyPollerInfo
	ts.NotEqual(int32(0), stickyPollerInfo.CurrentPollers)
	ts.False(stickyPollerInfo.IsAutoscaling)
	ts.assertRecentTimestamp(stickyPollerInfo.LastSuccessfulPollTime, 10*time.Second,
		"WorkflowStickyPollerInfo.LastSuccessfulPollTime after shutdown")

	nexusPollerInfo = workerInfo.NexusPollerInfo
	ts.Equal(int32(2), nexusPollerInfo.CurrentPollers)
	ts.False(nexusPollerInfo.IsAutoscaling)
	// Nexus poller has no successful polls since we didn't execute any nexus operations

	activityPollerInfo = workerInfo.ActivityPollerInfo
	ts.NotEqual(int32(0), activityPollerInfo.CurrentPollers)
	ts.False(activityPollerInfo.IsAutoscaling)
	ts.assertRecentTimestamp(activityPollerInfo.LastSuccessfulPollTime, 10*time.Second,
		"ActivityPollerInfo.LastSuccessfulPollTime after shutdown")

	ts.Equal(int32(1), workerInfo.TotalStickyCacheHit)
}

// TestWorkerHeartbeatDeploymentVersion verifies that deployment version info is
// included in heartbeats when versioning is enabled. This test doesn't run workflows
// since versioned workers require additional server-side setup for task routing.
func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatDeploymentVersion() {
	ctx := context.Background()

	taskQueue := ts.taskQueueName + "-deployment-version"

	w := worker.New(ts.client, taskQueue, worker.Options{
		DeploymentOptions: worker.DeploymentOptions{
			UseVersioning: true,
			Version: worker.WorkerDeploymentVersion{
				DeploymentName: "test-deployment",
				BuildID:        "test_build_id",
			},
			DefaultVersioningBehavior: internal.VersioningBehaviorAutoUpgrade,
		},
	})
	w.RegisterWorkflow(simpleWorkflow)
	ts.NoError(w.Start())
	defer w.Stop()

	// Wait for heartbeat to be sent
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, taskQueue)
		return workerInfo != nil && workerInfo.DeploymentVersion != nil
	}, 5*time.Second, 200*time.Millisecond, "Should find worker with deployment version")

	ts.NotNil(workerInfo.DeploymentVersion)
	ts.Equal("test_build_id", workerInfo.DeploymentVersion.BuildId)
	ts.Equal("test-deployment", workerInfo.DeploymentVersion.DeploymentName)
}

// TestWorkerHeartbeatDisabled verifies that when heartbeating is disabled,
// workers should not appear in ListWorkers
func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatDisabled() {
	ctx := context.Background()

	// Create a separate client with heartbeating disabled
	heartbeatInterval := time.Duration(0)
	clientNoHeartbeat, err := client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: &heartbeatInterval,
		ConnectionOptions:       client.ConnectionOptions{TLS: ts.config.TLS},
	})
	ts.NoError(err)
	defer clientNoHeartbeat.Close()

	taskQueueNoHeartbeat := taskQueuePrefix + "-no-heartbeat-" + ts.T().Name()

	// Create and start worker with no heartbeating
	workerNoHeartbeat := worker.New(clientNoHeartbeat, taskQueueNoHeartbeat, worker.Options{})
	workerNoHeartbeat.RegisterWorkflow(simpleWorkflow)
	ts.NoError(workerNoHeartbeat.Start())
	defer workerNoHeartbeat.Stop()

	// Wait a bit
	time.Sleep(2 * time.Second)

	// Get the internal client
	internalClient := clientNoHeartbeat.(internal.Client)
	workflowClient := internalClient.(*internal.WorkflowClient)

	// List workers - should not find the worker without heartbeating
	listResp, err := workflowClient.WorkflowService().ListWorkers(ctx, &workflowservice.ListWorkersRequest{
		Namespace: ts.config.Namespace,
		Query:     fmt.Sprintf(`TaskQueue="%s"`, taskQueueNoHeartbeat),
		PageSize:  10,
	})

	ts.NoError(err, "ListWorkers failed")
	foundWorker := false
	for _, workerInfo := range listResp.WorkersInfo {
		if workerInfo.WorkerHeartbeat.TaskQueue == taskQueueNoHeartbeat {
			foundWorker = true
			break
		}
	}
	ts.False(foundWorker, "Should not find worker without heartbeating enabled")
}

// Get worker info from the server
func (ts *WorkerHeartbeatTestSuite) getWorkerInfo(ctx context.Context, taskQueue string) *workerpb.WorkerHeartbeat {
	// Get the internal client to access the workflow service directly
	internalClient := ts.client.(internal.Client)
	workflowClient := internalClient.(*internal.WorkflowClient)

	// List workers in this namespace
	listResp, err := workflowClient.WorkflowService().ListWorkers(ctx, &workflowservice.ListWorkersRequest{
		Namespace: ts.config.Namespace,
		Query:     fmt.Sprintf(`TaskQueue="%s"`, taskQueue),
		PageSize:  10,
	})
	if err != nil {
		ts.T().Logf("ListWorkers failed: %v (may not be implemented on this server)", err)
		return nil
	}

	if len(listResp.WorkersInfo) == 0 {
		return nil
	}

	// Find our worker in the list
	var workerInstanceKey string
	for _, workerInfo := range listResp.WorkersInfo {
		if workerInfo.WorkerHeartbeat.TaskQueue == taskQueue {
			workerInstanceKey = workerInfo.WorkerHeartbeat.WorkerInstanceKey
			break
		}
	}

	if workerInstanceKey == "" {
		ts.T().Logf("Could not find worker with task queue %s in list", taskQueue)
		return nil
	}

	// Describe the specific worker
	describeResp, err := workflowClient.WorkflowService().DescribeWorker(ctx, &workflowservice.DescribeWorkerRequest{
		Namespace:         ts.config.Namespace,
		WorkerInstanceKey: workerInstanceKey,
	})
	if err != nil {
		ts.T().Logf("DescribeWorker failed: %v", err)
		return nil
	}

	return describeResp.WorkerInfo.WorkerHeartbeat
}

func (ts *WorkerHeartbeatTestSuite) logWorkerInfo(workerInfo *workerpb.WorkerHeartbeat) {
	ts.T().Logf("=== Worker Heartbeat Info ===")
	ts.T().Logf("Worker Instance Key: %s", workerInfo.WorkerInstanceKey)
	ts.T().Logf("Worker Identity: %s", workerInfo.WorkerIdentity)
	ts.T().Logf("Task Queue: %s", workerInfo.TaskQueue)
	ts.T().Logf("SDK Name: %s", workerInfo.SdkName)
	ts.T().Logf("SDK Version: %s", workerInfo.SdkVersion)
	ts.T().Logf("Status: %s", workerInfo.Status)
	ts.T().Logf("Total Sticky Cache Hit: %d", workerInfo.TotalStickyCacheHit)
	ts.T().Logf("Total Sticky Cache Miss: %d", workerInfo.TotalStickyCacheMiss)
	ts.T().Logf("Current Sticky Cache Size: %d", workerInfo.CurrentStickyCacheSize)
	if workerInfo.HostInfo != nil {
		ts.T().Logf("Host Name: %s", workerInfo.HostInfo.HostName)
		ts.T().Logf("Process ID: %s", workerInfo.HostInfo.ProcessId)
	}
	if workerInfo.WorkflowTaskSlotsInfo != nil {
		ts.T().Logf("=== Workflow Task Slots Info ===")
		ts.T().Logf("  Current Available Slots: %d", workerInfo.WorkflowTaskSlotsInfo.CurrentAvailableSlots)
		ts.T().Logf("  Current Used Slots: %d", workerInfo.WorkflowTaskSlotsInfo.CurrentUsedSlots)
		ts.T().Logf("  Slot Supplier Kind: %s", workerInfo.WorkflowTaskSlotsInfo.SlotSupplierKind)
		ts.T().Logf("  Total Processed Tasks: %d", workerInfo.WorkflowTaskSlotsInfo.TotalProcessedTasks)
		ts.T().Logf("  Total Failed Tasks: %d", workerInfo.WorkflowTaskSlotsInfo.TotalFailedTasks)
		ts.T().Logf("  Last Interval Processed: %d", workerInfo.WorkflowTaskSlotsInfo.LastIntervalProcessedTasks)
		ts.T().Logf("  Last Interval Failed: %d", workerInfo.WorkflowTaskSlotsInfo.LastIntervalFailureTasks)
	}
	if workerInfo.ActivityTaskSlotsInfo != nil {
		ts.T().Logf("=== Activity Task Slots Info ===")
		ts.T().Logf("  Current Available Slots: %d", workerInfo.ActivityTaskSlotsInfo.CurrentAvailableSlots)
		ts.T().Logf("  Current Used Slots: %d", workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots)
		ts.T().Logf("  Slot Supplier Kind: %s", workerInfo.ActivityTaskSlotsInfo.SlotSupplierKind)
		ts.T().Logf("  Total Processed Tasks: %d", workerInfo.ActivityTaskSlotsInfo.TotalProcessedTasks)
		ts.T().Logf("  Total Failed Tasks: %d", workerInfo.ActivityTaskSlotsInfo.TotalFailedTasks)
	}
	if workerInfo.LocalActivitySlotsInfo != nil {
		ts.T().Logf("=== Local Activity Slots Info ===")
		ts.T().Logf("  Current Available Slots: %d", workerInfo.LocalActivitySlotsInfo.CurrentAvailableSlots)
		ts.T().Logf("  Current Used Slots: %d", workerInfo.LocalActivitySlotsInfo.CurrentUsedSlots)
		ts.T().Logf("  Slot Supplier Kind: %s", workerInfo.LocalActivitySlotsInfo.SlotSupplierKind)
		ts.T().Logf("  Total Processed Tasks: %d", workerInfo.LocalActivitySlotsInfo.TotalProcessedTasks)
		ts.T().Logf("  Total Failed Tasks: %d", workerInfo.LocalActivitySlotsInfo.TotalFailedTasks)
	}
	if workerInfo.NexusTaskSlotsInfo != nil {
		ts.T().Logf("=== Nexus Task Slots Info ===")
		ts.T().Logf("  Current Available Slots: %d", workerInfo.NexusTaskSlotsInfo.CurrentAvailableSlots)
		ts.T().Logf("  Current Used Slots: %d", workerInfo.NexusTaskSlotsInfo.CurrentUsedSlots)
		ts.T().Logf("  Slot Supplier Kind: %s", workerInfo.NexusTaskSlotsInfo.SlotSupplierKind)
		ts.T().Logf("  Total Processed Tasks: %d", workerInfo.NexusTaskSlotsInfo.TotalProcessedTasks)
		ts.T().Logf("  Total Failed Tasks: %d", workerInfo.NexusTaskSlotsInfo.TotalFailedTasks)
	}
	if workerInfo.WorkflowPollerInfo != nil {
		ts.T().Logf("=== Workflow Poller Info ===")
		ts.T().Logf("  Current Pollers: %d", workerInfo.WorkflowPollerInfo.CurrentPollers)
		ts.T().Logf("  Last Successful Poll Time: %v", workerInfo.WorkflowPollerInfo.LastSuccessfulPollTime.AsTime())
		ts.T().Logf("  Is Autoscaling: %v", workerInfo.WorkflowPollerInfo.IsAutoscaling)
	}
	if workerInfo.WorkflowStickyPollerInfo != nil {
		ts.T().Logf("=== Workflow Sticky Poller Info ===")
		ts.T().Logf("  Current Pollers: %d", workerInfo.WorkflowStickyPollerInfo.CurrentPollers)
		ts.T().Logf("  Last Successful Poll Time: %v", workerInfo.WorkflowStickyPollerInfo.LastSuccessfulPollTime.AsTime())
		ts.T().Logf("  Is Autoscaling: %v", workerInfo.WorkflowStickyPollerInfo.IsAutoscaling)
	}
	if workerInfo.ActivityPollerInfo != nil {
		ts.T().Logf("=== Activity Poller Info ===")
		ts.T().Logf("  Current Pollers: %d", workerInfo.ActivityPollerInfo.CurrentPollers)
		ts.T().Logf("  Last Successful Poll Time: %v", workerInfo.ActivityPollerInfo.LastSuccessfulPollTime.AsTime())
		ts.T().Logf("  Is Autoscaling: %v", workerInfo.ActivityPollerInfo.IsAutoscaling)
	}
	if workerInfo.NexusPollerInfo != nil {
		ts.T().Logf("=== Nexus Poller Info ===")
		ts.T().Logf("  Current Pollers: %d", workerInfo.NexusPollerInfo.CurrentPollers)
		ts.T().Logf("  Last Successful Poll Time: %v", workerInfo.NexusPollerInfo.LastSuccessfulPollTime.AsTime())
		ts.T().Logf("  Is Autoscaling: %v", workerInfo.NexusPollerInfo.IsAutoscaling)
	}
	if len(workerInfo.Plugins) > 0 {
		ts.T().Logf("=== Plugins ===")
		for _, plugin := range workerInfo.Plugins {
			ts.T().Logf("  Name: %s", plugin.Name)
		}
	}
}

// Simple workflow for testing
func simpleWorkflow(ctx workflow.Context) (string, error) {
	return "hello", nil
}

// Simple nexus operation for testing - just returns immediately
var noopNexusOp = nexus.NewSyncOperation("noop", func(ctx context.Context, input nexus.NoValue, opts nexus.StartOperationOptions) (nexus.NoValue, error) {
	return nil, nil
})

var (
	blockingActivityStarted  = make(chan struct{}, 10)
	blockingActivityComplete = make(chan struct{}, 10)
)

func blockingActivity(ctx context.Context) (string, error) {
	// Signal that activity has started
	select {
	case blockingActivityStarted <- struct{}{}:
	default:
	}

	// Wait for signal to complete
	select {
	case <-blockingActivityComplete:
		return "done", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func workflowWithBlockingActivity(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result string
	err := workflow.ExecuteActivity(ctx, blockingActivity).Get(ctx, &result)
	return result, err
}

var failingActivityCallCount atomic.Int32

func failingActivity(ctx context.Context) error {
	failingActivityCallCount.Add(1)
	return temporal.NewApplicationError("intentional failure", "TEST_ERROR")
}

// Workflow that executes a failing activity with limited retries
func workflowWithFailingActivity(ctx workflow.Context) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	return workflow.ExecuteActivity(ctx, failingActivity).Get(ctx, nil)
}

// Workflow that panics (simulates workflow task failure)
var failingWorkflowShouldFail atomic.Bool

func failingWorkflow(ctx workflow.Context) (string, error) {
	if failingWorkflowShouldFail.Load() {
		return "", errors.New("intentional workflow failure")
	}
	return "success", nil
}

// TestWorkerHeartbeatWithActivityInFlight verifies that activity slots are tracked
// correctly when activities are in flight
func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatWithActivityInFlight() {
	ctx := context.Background()

	blockingActivityStarted = make(chan struct{}, 10)
	blockingActivityComplete = make(chan struct{}, 10)

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		MaxConcurrentActivityExecutionSize: 5,
	})
	ts.worker.RegisterWorkflow(workflowWithBlockingActivity)
	ts.worker.RegisterActivity(blockingActivity)
	ts.NoError(ts.worker.Start())

	workflowOptions := client.StartWorkflowOptions{
		ID:        "test-activity-in-flight-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}

	run, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, workflowWithBlockingActivity)
	ts.NoError(err)

	// Wait for activity to start
	select {
	case <-blockingActivityStarted:
		ts.T().Log("Activity started")
	case <-time.After(10 * time.Second):
		ts.Fail("Timeout waiting for activity to start")
	}

	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.ActivityTaskSlotsInfo != nil &&
			workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should have at least 1 activity slot used")

	ts.T().Logf("Activity slots used: %d, available: %d",
		workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots,
		workerInfo.ActivityTaskSlotsInfo.CurrentAvailableSlots)
	ts.GreaterOrEqual(workerInfo.ActivityTaskSlotsInfo.CurrentAvailableSlots, int32(0))

	blockingActivityComplete <- struct{}{}

	var result string
	err = run.Get(ctx, &result)
	ts.NoError(err)
	ts.Equal("done", result)

	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.ActivityTaskSlotsInfo != nil &&
			workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots == 0
	}, 5*time.Second, 200*time.Millisecond, "Activity slot should be released after completion")

	ts.T().Logf("After completion - Activity slots used: %d, available: %d",
		workerInfo.ActivityTaskSlotsInfo.CurrentUsedSlots,
		workerInfo.ActivityTaskSlotsInfo.CurrentAvailableSlots)
	ts.GreaterOrEqual(workerInfo.ActivityTaskSlotsInfo.TotalProcessedTasks, int32(1))
}

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatStickyCacheMiss() {
	ctx := context.Background()

	wf1ActivityStarted := make(chan struct{}, 1)
	wf1ActivityComplete := make(chan struct{}, 1)
	wf2ActivityStarted := make(chan struct{}, 1)
	wf2ActivityComplete := make(chan struct{}, 1)

	stickyCacheMissActivity := func(ctx context.Context, marker string) (string, error) {
		switch marker {
		case "wf1":
			select {
			case wf1ActivityStarted <- struct{}{}:
			default:
			}
			select {
			case <-wf1ActivityComplete:
				return marker, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		case "wf2":
			select {
			case wf2ActivityStarted <- struct{}{}:
			default:
			}
			select {
			case <-wf2ActivityComplete:
				return marker, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		return marker, nil
	}

	stickyCacheMissWorkflow := func(ctx workflow.Context, marker string) (string, error) {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		var result string
		err := workflow.ExecuteActivity(ctx, stickyCacheMissActivity, marker).Get(ctx, &result)
		return result, err
	}

	// GC ensures previous worker's cache finalizer runs, allowing cache to be recreated with new size
	runtime.GC()
	worker.SetStickyWorkflowCacheSize(1)
	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		MaxConcurrentWorkflowTaskExecutionSize: 2,
		DisableEagerActivities:                 true,
	})
	ts.worker.RegisterWorkflow(stickyCacheMissWorkflow)
	ts.worker.RegisterActivity(stickyCacheMissActivity)
	ts.NoError(ts.worker.Start())

	wf1Options := client.StartWorkflowOptions{
		ID:        "test-sticky-miss-wf1-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}
	run1, err := ts.client.ExecuteWorkflow(ctx, wf1Options, stickyCacheMissWorkflow, "wf1")
	ts.NoError(err)

	select {
	case <-wf1ActivityStarted:
		ts.T().Log("wf1 activity started")
	case <-time.After(10 * time.Second):
		ts.Fail("Timeout waiting for wf1 activity to start")
	}

	// this should evict wf1 from the cache
	wf2Options := client.StartWorkflowOptions{
		ID:        "test-sticky-miss-wf2-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}
	run2, err := ts.client.ExecuteWorkflow(ctx, wf2Options, stickyCacheMissWorkflow, "wf2")
	ts.NoError(err)

	select {
	case <-wf2ActivityStarted:
		ts.T().Log("wf2 activity started")
	case <-time.After(10 * time.Second):
		ts.Fail("Timeout waiting for wf2 activity to start")
	}

	// wf1 should experience a cache miss when it resumes
	wf1ActivityComplete <- struct{}{}
	var result1 string
	ts.NoError(run1.Get(ctx, &result1))
	ts.Equal("wf1", result1)

	wf2ActivityComplete <- struct{}{}
	var result2 string
	ts.NoError(run2.Get(ctx, &result2))
	ts.Equal("wf2", result2)

	// Wait for heartbeat to capture sticky cache miss
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.TotalStickyCacheMiss >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should have at least 1 sticky cache miss")
}

// TestWorkerHeartbeatMultipleWorkers verifies that multiple workers can heartbeat
// simultaneously and be tracked separately
func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatMultipleWorkers() {
	ctx := context.Background()

	taskQueue1 := ts.taskQueueName + "-worker1"
	taskQueue2 := ts.taskQueueName + "-worker2"

	worker1 := worker.New(ts.client, taskQueue1, worker.Options{})
	worker1.RegisterWorkflow(simpleWorkflow)
	ts.NoError(worker1.Start())
	defer worker1.Stop()

	worker2 := worker.New(ts.client, taskQueue2, worker.Options{})
	worker2.RegisterWorkflow(simpleWorkflow)
	ts.NoError(worker2.Start())
	defer worker2.Stop()

	// Run workflow on each worker
	var wg sync.WaitGroup
	for i, tq := range []string{taskQueue1, taskQueue2} {
		wg.Add(1)
		go func(idx int, taskQueue string) {
			defer wg.Done()
			workflowOptions := client.StartWorkflowOptions{
				ID:        fmt.Sprintf("test-multi-worker-%d-%s", idx, uuid.NewString()),
				TaskQueue: taskQueue,
			}
			run, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, simpleWorkflow)
			ts.NoError(err)
			err = run.Get(ctx, nil)
			ts.NoError(err)
		}(i, tq)
	}
	wg.Wait()

	// Verify both workers are tracked
	var workerInfo1, workerInfo2 *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo1 = ts.getWorkerInfo(ctx, taskQueue1)
		workerInfo2 = ts.getWorkerInfo(ctx, taskQueue2)
		return workerInfo1 != nil && workerInfo2 != nil
	}, 5*time.Second, 200*time.Millisecond, "Should find both workers")

	ts.NotEqual(workerInfo1.WorkerInstanceKey, workerInfo2.WorkerInstanceKey,
		"Different workers should have different instance keys")

	ts.Equal(taskQueue1, workerInfo1.TaskQueue)
	ts.Equal(taskQueue2, workerInfo2.TaskQueue)

	ts.Equal(workerInfo1.HostInfo.WorkerGroupingKey, workerInfo2.HostInfo.WorkerGroupingKey,
		"Workers should share the same client and worker grouping key")

}

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatFailureMetrics() {
	ctx := context.Background()

	// Reset call counter
	failingActivityCallCount.Store(0)

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(workflowWithFailingActivity)
	ts.worker.RegisterActivity(failingActivity)
	ts.NoError(ts.worker.Start())

	// Run workflow that will have a failing activity
	workflowOptions := client.StartWorkflowOptions{
		ID:        "test-failure-metrics-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}

	run, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, workflowWithFailingActivity)
	ts.NoError(err)

	// Wait for workflow to complete (will fail due to activity failure)
	err = run.Get(ctx, nil)
	ts.Error(err)

	// Wait for heartbeat to capture failure metrics
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.ActivityTaskSlotsInfo != nil &&
			workerInfo.ActivityTaskSlotsInfo.TotalFailedTasks >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should have tracked at least 1 activity task failure")

	ts.GreaterOrEqual(workerInfo.ActivityTaskSlotsInfo.LastIntervalFailureTasks, int32(1))

	// Last interval should go back to 0 on next heartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.ActivityTaskSlotsInfo != nil &&
			workerInfo.ActivityTaskSlotsInfo.LastIntervalFailureTasks == 0
	}, 5*time.Second, 200*time.Millisecond, "Last interval failure count should reset to 0")
}

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatWorkflowTaskProcessed() {
	ctx := context.Background()

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(simpleWorkflow)
	ts.NoError(ts.worker.Start())

	numWorkflows := 3
	for i := 0; i < numWorkflows; i++ {
		workflowOptions := client.StartWorkflowOptions{
			ID:        fmt.Sprintf("test-wf-processed-%d-%s", i, uuid.NewString()),
			TaskQueue: ts.taskQueueName,
		}
		run, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, simpleWorkflow)
		ts.NoError(err)
		err = run.Get(ctx, nil)
		ts.NoError(err)
	}

	// Wait for heartbeat to capture processed tasks
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.TotalProcessedTasks == int32(numWorkflows)
	}, 5*time.Second, 200*time.Millisecond, "Should have processed all workflow tasks")

	ts.GreaterOrEqual(workerInfo.WorkflowTaskSlotsInfo.LastIntervalProcessedTasks, int32(1))

	// Last interval should go back to 0 on next heartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.LastIntervalProcessedTasks == 0
	}, 5*time.Second, 200*time.Millisecond, "Last interval processed count should reset to 0")
}

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatResourceBasedTuner() {
	ctx := context.Background()

	tuner, err := worker.NewResourceBasedTuner(worker.ResourceBasedTunerOptions{
		TargetMem:    0.8,
		TargetCpu:    0.9,
		InfoSupplier: sysinfo.SysInfoProvider(),
	})
	ts.NoError(err)

	tunerWorkflow := func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return workflow.ExecuteActivity(ctx, "tunerActivity").Get(ctx, nil)
	}

	tunerActivity := func(ctx context.Context) error {
		activity.GetLogger(ctx).Info("tunerActivity executed")
		return nil
	}

	autoscalingBehavior := worker.NewPollerBehaviorAutoscaling(worker.PollerBehaviorAutoscalingOptions{
		InitialNumberOfPollers: 5,
		MinimumNumberOfPollers: 1,
		MaximumNumberOfPollers: 200,
	})

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		Tuner:                      tuner,
		WorkflowTaskPollerBehavior: autoscalingBehavior,
		ActivityTaskPollerBehavior: autoscalingBehavior,
		NexusTaskPollerBehavior:    autoscalingBehavior,
	})
	ts.worker.RegisterWorkflowWithOptions(tunerWorkflow, workflow.RegisterOptions{Name: "tunerWorkflow"})
	ts.worker.RegisterActivityWithOptions(tunerActivity, activity.RegisterOptions{Name: "tunerActivity"})
	ts.NoError(ts.worker.Start())

	// Run a workflow
	workflowOptions := client.StartWorkflowOptions{
		ID:        "test-resource-tuner-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}
	run, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, "tunerWorkflow")
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))

	// Wait for heartbeat with resource-based tuner info
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.SlotSupplierKind == "ResourceBased"
	}, 5*time.Second, 200*time.Millisecond, "Should find worker with ResourceBased slot supplier")

	ts.NotNil(workerInfo.ActivityTaskSlotsInfo)
	ts.Equal("ResourceBased", workerInfo.ActivityTaskSlotsInfo.SlotSupplierKind)

	ts.NotNil(workerInfo.LocalActivitySlotsInfo)
	ts.Equal("ResourceBased", workerInfo.LocalActivitySlotsInfo.SlotSupplierKind)

	ts.NotNil(workerInfo.WorkflowPollerInfo)
	ts.True(workerInfo.WorkflowPollerInfo.IsAutoscaling)

	ts.NotNil(workerInfo.WorkflowStickyPollerInfo)
	ts.True(workerInfo.WorkflowStickyPollerInfo.IsAutoscaling)

	ts.NotNil(workerInfo.ActivityPollerInfo)
	ts.True(workerInfo.ActivityPollerInfo.IsAutoscaling)
}

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatPlugins() {
	ctx := context.Background()

	clientPlugin, err := temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "test-client-plugin",
	})
	ts.NoError(err)

	workerPlugin, err := temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "test-worker-plugin",
	})
	ts.NoError(err)

	duplicatePlugin, err := temporal.NewSimplePlugin(temporal.SimplePluginOptions{
		Name: "test-client-plugin",
	})
	ts.NoError(err)

	// Create a new client with the plugin
	heartbeatInterval := 1 * time.Second
	pluginClient, err := client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: &heartbeatInterval,
		ConnectionOptions:       client.ConnectionOptions{TLS: ts.config.TLS},
		Identity:                "PluginTest",
		Plugins:                 []client.Plugin{clientPlugin},
	})
	ts.NoError(err)
	defer pluginClient.Close()

	// Create worker with additional plugins (including duplicate)
	ts.worker = worker.New(pluginClient, ts.taskQueueName, worker.Options{
		Plugins: []worker.Plugin{workerPlugin, duplicatePlugin},
	})
	ts.worker.RegisterWorkflow(simpleWorkflow)
	ts.NoError(ts.worker.Start())

	workflowOptions := client.StartWorkflowOptions{
		ID:        "test-plugins-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}
	run, err := pluginClient.ExecuteWorkflow(ctx, workflowOptions, simpleWorkflow)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))

	// Wait for heartbeat with plugin info
	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && len(workerInfo.Plugins) == 2
	}, 5*time.Second, 200*time.Millisecond, "Should have 2 unique plugins (duplicates deduped)")

	pluginNames := make(map[string]bool)
	for _, plugin := range workerInfo.Plugins {
		pluginNames[plugin.Name] = true
	}
	ts.True(pluginNames["test-client-plugin"])
	ts.True(pluginNames["test-worker-plugin"])
}
