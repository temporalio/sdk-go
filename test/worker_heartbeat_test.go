package test_test

import (
	"context"
	"fmt"
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
	// Create a client with heartbeating enabled
	ts.client, err = client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: 1 * time.Second,
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

	var workerInfo *workerpb.WorkerHeartbeat
	// Wait for heartbeat to capture the in-flight activity
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
	ts.NotEqual(int32(0), workflowPollerInfo.CurrentPollers)
	nexusPollerInfo := workerInfo.NexusPollerInfo
	ts.NotEqual(int32(0), nexusPollerInfo.CurrentPollers)
	activityPollerInfo := workerInfo.ActivityPollerInfo
	ts.NotEqual(int32(0), activityPollerInfo.CurrentPollers)

	if ts.config.maxWorkflowCacheSize > 0 {
		stickyPollerInfo := workerInfo.WorkflowStickyPollerInfo
		ts.NotEqual(int32(0), stickyPollerInfo.CurrentPollers)
		ts.GreaterOrEqual(workerInfo.CurrentStickyCacheSize, int32(1))
	}

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
	ts.NotEqual(int32(0), workflowPollerInfo.CurrentPollers)
	ts.False(workflowPollerInfo.IsAutoscaling)
	ts.assertRecentTimestamp(workflowPollerInfo.LastSuccessfulPollTime, 10*time.Second,
		"WorkflowPollerInfo.LastSuccessfulPollTime after shutdown")

	if ts.config.maxWorkflowCacheSize > 0 {
		stickyPollerInfo := workerInfo.WorkflowStickyPollerInfo
		ts.NotEqual(int32(0), stickyPollerInfo.CurrentPollers)
		ts.False(stickyPollerInfo.IsAutoscaling)
		ts.assertRecentTimestamp(stickyPollerInfo.LastSuccessfulPollTime, 10*time.Second,
			"WorkflowStickyPollerInfo.LastSuccessfulPollTime after shutdown")
	}

	nexusPollerInfo = workerInfo.NexusPollerInfo
	ts.NotEqual(int32(0), nexusPollerInfo.CurrentPollers)
	ts.False(nexusPollerInfo.IsAutoscaling)
	// Nexus poller has no successful polls since we didn't execute any nexus operations

	activityPollerInfo = workerInfo.ActivityPollerInfo
	ts.NotEqual(int32(0), activityPollerInfo.CurrentPollers)
	ts.False(activityPollerInfo.IsAutoscaling)
	ts.assertRecentTimestamp(activityPollerInfo.LastSuccessfulPollTime, 10*time.Second,
		"ActivityPollerInfo.LastSuccessfulPollTime after shutdown")

	if ts.config.maxWorkflowCacheSize > 0 {
		ts.GreaterOrEqual(workerInfo.TotalStickyCacheHit, int32(1))
	}
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
	clientNoHeartbeat, err := client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: -1,
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

// Workflow that panics to simulate a workflow task failure. The flag controls
// whether it panics, allowing tests to toggle it off so the workflow can
// eventually complete after the server retries the task.
var failingWorkflowShouldFail atomic.Bool

func failingWorkflow(ctx workflow.Context) (string, error) {
	if failingWorkflowShouldFail.Load() {
		panic("intentional workflow task failure")
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
	if ts.config.maxWorkflowCacheSize == 0 {
		ts.T().Skip("Sticky cache disabled")
	}
	ctx := context.Background()

	activityStarted := make(chan struct{}, 1)
	activityComplete := make(chan struct{}, 1)

	cacheMissActivity := func(ctx context.Context) (string, error) {
		select {
		case activityStarted <- struct{}{}:
		default:
		}
		select {
		case <-activityComplete:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	cacheMissWorkflow := func(ctx workflow.Context) (string, error) {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		var result string
		err := workflow.ExecuteActivity(ctx, cacheMissActivity).Get(ctx, &result)
		return result, err
	}

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{
		DisableEagerActivities: true,
	})
	ts.worker.RegisterWorkflow(cacheMissWorkflow)
	ts.worker.RegisterActivity(cacheMissActivity)
	ts.NoError(ts.worker.Start())

	wfOptions := client.StartWorkflowOptions{
		ID:        "test-sticky-miss-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}
	run, err := ts.client.ExecuteWorkflow(ctx, wfOptions, cacheMissWorkflow)
	ts.NoError(err)

	select {
	case <-activityStarted:
		ts.T().Log("Activity started")
	case <-time.After(10 * time.Second):
		ts.Fail("Timeout waiting for activity to start")
	}

	// Purge the cache so the workflow's sticky task triggers a cache miss on resume
	worker.PurgeStickyWorkflowCache()

	activityComplete <- struct{}{}
	var result string
	ts.NoError(run.Get(ctx, &result))
	ts.Equal("done", result)

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

func (ts *WorkerHeartbeatTestSuite) TestWorkerHeartbeatWorkflowTaskFailureMetrics() {
	ctx := context.Background()

	failingWorkflowShouldFail.Store(true)
	defer failingWorkflowShouldFail.Store(false)

	ts.worker = worker.New(ts.client, ts.taskQueueName, worker.Options{})
	ts.worker.RegisterWorkflow(failingWorkflow)
	ts.NoError(ts.worker.Start())

	workflowOptions := client.StartWorkflowOptions{
		ID:        "test-wf-task-failure-" + uuid.NewString(),
		TaskQueue: ts.taskQueueName,
	}

	_, err := ts.client.ExecuteWorkflow(ctx, workflowOptions, failingWorkflow)
	ts.NoError(err)

	var workerInfo *workerpb.WorkerHeartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.TotalFailedTasks >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should have tracked at least 1 workflow task failure")

	ts.GreaterOrEqual(workerInfo.WorkflowTaskSlotsInfo.TotalFailedTasks, int32(1))
	ts.GreaterOrEqual(workerInfo.WorkflowTaskSlotsInfo.LastIntervalFailureTasks, int32(1))

	// Stop panicking so the workflow can complete on the next retry
	failingWorkflowShouldFail.Store(false)

	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.TotalProcessedTasks >= 1
	}, 5*time.Second, 200*time.Millisecond, "Should have processed at least 1 workflow task after recovery")

	// Last interval failure count should reset to 0 on a subsequent heartbeat
	ts.Eventually(func() bool {
		workerInfo = ts.getWorkerInfo(ctx, ts.taskQueueName)
		return workerInfo != nil && workerInfo.WorkflowTaskSlotsInfo != nil &&
			workerInfo.WorkflowTaskSlotsInfo.LastIntervalFailureTasks == 0
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

	if ts.config.maxWorkflowCacheSize > 0 {
		ts.NotNil(workerInfo.WorkflowStickyPollerInfo)
		ts.True(workerInfo.WorkflowStickyPollerInfo.IsAutoscaling)
	}

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
	pluginClient, err := client.Dial(client.Options{
		HostPort:                ts.config.ServiceAddr,
		Namespace:               ts.config.Namespace,
		Logger:                  ilog.NewDefaultLogger(),
		WorkerHeartbeatInterval: 1 * time.Second,
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
