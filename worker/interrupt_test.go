package worker_test

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func TestInterruptChWorkerRun(t *testing.T) {
	if signalName := os.Getenv("TEMPORAL_TEST_WORKER_SIGNAL"); signalName != "" {
		testInterruptChWorkerRun(t, signalName)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support sending SIGINT or SIGTERM with Process.Signal")
	}

	for _, signalName := range []string{"SIGINT", "SIGTERM"} {
		t.Run(signalName, func(t *testing.T) {
			// Signal only a subprocess, so registrations and signals cannot affect other tests.
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInterruptChWorkerRun$", "-test.timeout=20s")
			cmd.Env = append(os.Environ(), "TEMPORAL_TEST_WORKER_SIGNAL="+signalName)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func testInterruptChWorkerRun(t *testing.T, signalName string) {
	var sig os.Signal
	switch signalName {
	case "SIGINT":
		sig = os.Interrupt
	case "SIGTERM":
		sig = syscall.SIGTERM
	default:
		t.Fatalf("unsupported worker signal %q", signalName)
	}

	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.DescribeNamespaceResponse{
			NamespaceInfo: &namespacepb.NamespaceInfo{State: enumspb.NAMESPACE_STATE_REGISTERED},
		}, nil).AnyTimes()

	pollsStopped := make(chan struct{})
	var stopPolls sync.Once
	service.EXPECT().ShutdownWorker(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *workflowservice.ShutdownWorkerRequest, ...grpc.CallOption) (*workflowservice.ShutdownWorkerResponse, error) {
			stopPolls.Do(func() { close(pollsStopped) })
			return &workflowservice.ShutdownWorkerResponse{}, nil
		}).Times(3) // Main queue and both session activity queues.
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *workflowservice.PollWorkflowTaskQueueRequest, ...grpc.CallOption) (*workflowservice.PollWorkflowTaskQueueResponse, error) {
			<-pollsStopped
			return &workflowservice.PollWorkflowTaskQueueResponse{}, nil
		}).AnyTimes()

	var firstPoll sync.Once
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.PollActivityTaskQueueRequest, _ ...grpc.CallOption) (*workflowservice.PollActivityTaskQueueResponse, error) {
			var task *workflowservice.PollActivityTaskQueueResponse
			if request.TaskQueue.Name == "signal-test" {
				firstPoll.Do(func() {
					now := timestamppb.Now()
					task = &workflowservice.PollActivityTaskQueueResponse{
						TaskToken:              []byte("task-token"),
						WorkflowExecution:      &commonpb.WorkflowExecution{WorkflowId: "workflow-id", RunId: "run-id"},
						WorkflowType:           &commonpb.WorkflowType{Name: "workflow"},
						ActivityType:           &commonpb.ActivityType{Name: "activity"},
						ActivityId:             "activity-id",
						ScheduledTime:          now,
						StartedTime:            now,
						StartToCloseTimeout:    durationpb.New(time.Minute),
						ScheduleToCloseTimeout: durationpb.New(time.Minute),
					}
				})
			}
			if task != nil {
				return task, nil
			}
			<-pollsStopped
			return &workflowservice.PollActivityTaskQueueResponse{}, nil
		}).AnyTimes()
	completed := make(chan *workflowservice.RespondActivityTaskCompletedRequest, 1)
	service.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, request *workflowservice.RespondActivityTaskCompletedRequest, _ ...grpc.CallOption) (*workflowservice.RespondActivityTaskCompletedResponse, error) {
			completed <- request
			return &workflowservice.RespondActivityTaskCompletedResponse{}, nil
		}).Times(1)

	client := internal.NewServiceClient(service, nil, internal.ClientOptions{Logger: ilog.NewNopLogger()})
	w := worker.New(client, "signal-test", worker.Options{
		WorkerStopTimeout:   10 * time.Second,
		EnableSessionWorker: true,
	})
	w.RegisterWorkflowWithOptions(func(workflow.Context) error { return nil }, workflow.RegisterOptions{Name: "workflow"})
	started := make(chan context.Context, 1)
	stopping := make(chan struct{})
	finishActivity := make(chan struct{})
	w.RegisterActivityWithOptions(func(ctx context.Context) (string, error) {
		started <- ctx
		<-activity.GetWorkerStopChannel(ctx)
		close(stopping)
		<-finishActivity
		return "completed", ctx.Err()
	}, activity.RegisterOptions{Name: "activity"})

	interruptCh := worker.InterruptCh()
	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(interruptCh) }()
	activityCtx := <-started
	process, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	defer func() { _ = process.Release() }()
	require.NoError(t, process.Signal(sig))
	<-stopping
	require.NoError(t, activityCtx.Err(), "signal must allow the activity to finish before cancellation")
	select {
	case err := <-runDone:
		t.Fatalf("Run returned before the in-flight activity completed: %v", err)
	default:
	}
	close(finishActivity)
	require.NoError(t, <-runDone)
	select {
	case request := <-completed:
		var result string
		require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(request.Result, &result))
		require.Equal(t, "completed", result)
	default:
		t.Fatal("Run returned without reporting the activity completion")
	}
}
