package internal

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	workerpb "go.temporal.io/api/worker/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/grpc"
)

// TestStopCancelsInFlightHeartbeatRPC verifies that calling stop() on a
// sharedNamespaceWorker cancels an in-flight heartbeat RPC. Without the fix
// (using context.Background() for the RPC), stop() would hang forever because
// the blocked RPC prevents run() from seeing stopC. With the fix
// (heartbeatCtx), stop() cancels the context first, unblocking the RPC.
func TestStopCancelsInFlightHeartbeatRPC(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(ctrl)

	mockService.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&workflowservice.GetSystemInfoResponse{}, nil).AnyTimes()

	// Simulate an RPC that blocks until its context is cancelled.
	heartbeatStarted := make(chan struct{})
	mockService.EXPECT().RecordWorkerHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *workflowservice.RecordWorkerHeartbeatRequest, _ ...grpc.CallOption) (*workflowservice.RecordWorkerHeartbeatResponse, error) {
			close(heartbeatStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		}).AnyTimes()

	wfClient := NewServiceClient(mockService, nil, ClientOptions{})

	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	hw := &sharedNamespaceWorker{
		client:          wfClient,
		namespace:       "test-ns",
		interval:        50 * time.Millisecond,
		heartbeatCtx:    heartbeatCtx,
		heartbeatCancel: heartbeatCancel,
		callbacks: map[string]func() *workerpb.WorkerHeartbeat{
			"worker1": func() *workerpb.WorkerHeartbeat { return &workerpb.WorkerHeartbeat{} },
		},
		stopC:    make(chan struct{}),
		stoppedC: make(chan struct{}),
		logger:   ilog.NewDefaultLogger(),
	}
	hw.started.Store(true)
	go hw.run()

	// Wait for the heartbeat RPC to be in-flight.
	select {
	case <-heartbeatStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for heartbeat RPC to start")
	}

	// stop() should return promptly because heartbeatCancel() unblocks the
	// in-flight RPC. Without the fix, this hangs forever.
	done := make(chan struct{})
	go func() {
		hw.stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stop() — in-flight heartbeat RPC was not cancelled")
	}
}
