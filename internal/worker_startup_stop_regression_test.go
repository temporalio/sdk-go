// This test calls Stop from the real Nexus tuner callback during Start.
// It checks that startup rejects the next resource change instead of creating
// a Nexus worker after cleanup has already finished.
package internal

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	genenumspb "go.temporal.io/api/enums/v1"
	gennamespacepb "go.temporal.io/api/namespace/v1"
	genworkflowservice "go.temporal.io/api/workflowservice/v1"
	genworkflowservicemock "go.temporal.io/api/workflowservicemock/v1"
	"google.golang.org/grpc"
)

type (
	startupStoppingNexusTuner struct {
		WorkerTuner
		stop func()
	}
)

func TestWorkerStartupAdmissionStopFromNexusTuner(t *testing.T) {
	service := genworkflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.DescribeNamespaceResponse{
			NamespaceInfo: &gennamespacepb.NamespaceInfo{
				Name: "startup-stop-test", State: genenumspb.NAMESPACE_STATE_REGISTERED,
			},
		}, nil).AnyTimes()
	service.EXPECT().ShutdownWorker(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.ShutdownWorkerResponse{}, nil).AnyTimes()
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.PollActivityTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollActivityTaskQueueResponse, error) {
			<-ctx.Done()
			return &genworkflowservice.PollActivityTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *genworkflowservice.PollNexusTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollNexusTaskQueueResponse, error) {
			<-ctx.Done()
			return &genworkflowservice.PollNexusTaskQueueResponse{}, nil
		}).AnyTimes()
	cli := NewServiceClient(service, nil, ClientOptions{
		Namespace: "startup-stop-test", WorkerHeartbeatInterval: -1,
	})
	fixed, err := NewFixedSizeTuner(FixedSizeTunerOptions{
		NumWorkflowSlots: 4, NumActivitySlots: 4, NumLocalActivitySlots: 4, NumNexusSlots: 4,
	})
	require.NoError(t, err)
	tuner := &startupStoppingNexusTuner{WorkerTuner: fixed}
	w := NewAggregatedWorker(cli, "startup-stop-test", WorkerOptions{
		DisableWorkflowWorker: true, Tuner: tuner, WorkerStopTimeout: time.Second,
	})
	tuner.stop = w.Stop
	t.Cleanup(func() {
		w.Stop()
		// The unchanged SDK can create this child after Stop returned. Clean it
		// directly so the reproduction does not leave its polling loops alive.
		if w.nexusWorker != nil {
			w.nexusWorker.Stop()
		}
		cli.Close()
	})
	w.RegisterActivityWithOptions(func(context.Context) error { return nil }, RegisterActivityOptions{
		Name: "StartupStopActivity",
	})
	nexusService := nexus.NewService("StartupStopService")
	require.NoError(t, nexusService.Register(nexus.NewSyncOperation(
		"echo", func(_ context.Context, input string, _ nexus.StartOperationOptions) (string, error) {
			return input, nil
		},
	)))
	w.RegisterNexusService(nexusService)

	err = w.Start()
	assert.ErrorIs(t, err, ErrWorkerShutdown)
	assert.Nil(t, w.nexusWorker, "Start created a Nexus worker after Stop returned")
}

// The SDK requests this supplier during startup. Stop finishes before the
// supplier returns, so the test can observe any resource created afterward.
func (tuner *startupStoppingNexusTuner) GetNexusSlotSupplier() SlotSupplier {
	tuner.stop()
	return tuner.WorkerTuner.GetNexusSlotSupplier()
}
