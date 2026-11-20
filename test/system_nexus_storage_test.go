package test_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type systemNexusStorageSelector struct {
	mu       sync.Mutex
	drivers  map[string]converter.StorageDriver
	contexts []converter.StorageDriverSelectContext
}

func (s *systemNexusStorageSelector) SelectDriver(ctx converter.StorageDriverSelectContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts = append(s.contexts, ctx)
	if target, ok := ctx.Target.(converter.StorageDriverWorkflowInfo); ok {
		return s.drivers[target.WorkflowID], nil
	}
	return nil, nil
}

func systemNexusStorageTarget(ctx workflow.Context, input string) ([]string, error) {
	values := []string{input}
	for range 2 {
		var signal string
		workflow.GetSignalChannel(ctx, "storage-signal").Receive(ctx, &signal)
		values = append(values, signal)
	}
	return values, nil
}

func systemNexusStorageCaller(ctx workflow.Context, ids []string) error {
	for round := range 2 {
		futures := []workflow.Future{
			workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{},
				ids[0], "storage-signal", fmt.Sprintf("signal-0-%d", round), "StorageTargetA", "start-0"),
			workflow.SignalWithStartWorkflow(ctx, workflow.SignalWithStartWorkflowOptions{},
				ids[1], "storage-signal", fmt.Sprintf("signal-1-%d", round), "StorageTargetB", "start-1"),
		}
		for _, future := range futures {
			if err := future.Get(ctx, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ts *SystemNexusTestSuite) TestWorkflowSignalWithStartStorageRouting() {
	ids := []string{ts.workflowID("storage-a"), ts.workflowID("storage-b")}
	callerID := ts.workflowID("storage-caller")
	drivers := []*memStorageDriver{newMemDriver("target-a"), newMemDriver("target-b")}
	selector := &systemNexusStorageSelector{drivers: map[string]converter.StorageDriver{
		ids[0]: drivers[0], ids[1]: drivers[1],
	}}
	storage := converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{drivers[0], drivers[1]},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	}
	c, err := ts.newIntegrationTestClient(func(options *client.Options) {
		options.ExternalStorage = storage
	})
	ts.NoError(err)
	defer c.Close()
	queue := ts.taskQueueName + "-storage"
	w := worker.New(c, queue, worker.Options{WorkflowPanicPolicy: worker.FailWorkflow})
	w.RegisterWorkflow(systemNexusStorageCaller)
	w.RegisterWorkflowWithOptions(systemNexusStorageTarget, workflow.RegisterOptions{Name: "StorageTargetA", DisableAlreadyRegisteredCheck: true})
	w.RegisterWorkflowWithOptions(systemNexusStorageTarget, workflow.RegisterOptions{Name: "StorageTargetB", DisableAlreadyRegisteredCheck: true})
	ts.NoError(w.Start())
	defer w.Stop()
	ctx, cancel := context.WithTimeout(ts.T().Context(), 30*time.Second)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: callerID, TaskQueue: queue}, systemNexusStorageCaller, ids)
	ts.NoError(err)
	ts.NoError(run.Get(ctx, nil))
	for index, id := range ids {
		var values []string
		ts.NoError(c.GetWorkflow(ctx, id, "").Get(ctx, &values))
		ts.Equal([]string{fmt.Sprintf("start-%d", index), fmt.Sprintf("signal-%d-0", index), fmt.Sprintf("signal-%d-1", index)}, values)
		contexts := drivers[index].getStoreContexts()
		ts.NotEmpty(contexts)
		foundUnboundRun := false
		for _, storeContext := range contexts {
			ts.Equal(ts.config.Namespace, storeContext.Namespace)
			target, ok := storeContext.Target.(converter.StorageDriverWorkflowInfo)
			ts.True(ok)
			ts.Equal(ts.config.Namespace, target.Namespace)
			ts.Equal(id, target.WorkflowID)
			ts.Equal([]string{"StorageTargetA", "StorageTargetB"}[index], target.WorkflowType)
			foundUnboundRun = foundUnboundRun || target.RunID == ""
		}
		ts.True(foundUnboundRun)
		_, retrieved := drivers[index].getStoreCounts()
		ts.Positive(retrieved)
	}
	selector.mu.Lock()
	selections := append([]converter.StorageDriverSelectContext(nil), selector.contexts...)
	selector.mu.Unlock()
	for _, selected := range selections {
		ts.Equal(ts.config.Namespace, selected.Namespace)
	}
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{ExternalStorage: storage})
	ts.NoError(err)
	replayer.RegisterWorkflow(systemNexusStorageCaller)
	replayer.RegisterWorkflowWithOptions(systemNexusStorageTarget, workflow.RegisterOptions{Name: "StorageTargetA", DisableAlreadyRegisteredCheck: true})
	replayer.RegisterWorkflowWithOptions(systemNexusStorageTarget, workflow.RegisterOptions{Name: "StorageTargetB", DisableAlreadyRegisteredCheck: true})
	for _, id := range append([]string{callerID}, ids...) {
		ts.NoError(replayer.ReplayWorkflowExecution(ctx, c.WorkflowService(), nil, ts.config.Namespace, workflow.Execution{ID: id}))
	}
}
