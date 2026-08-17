package test_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/operatorservice/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const extStoreNexusServiceName = "test"

// sizeOp returns the length of its input; exercised with a large (offloaded) input.
var extStoreNexusSizeOp = nexus.NewSyncOperation(
	"size-op",
	func(ctx context.Context, input string, _ nexus.StartOperationOptions) (int, error) {
		return len(input), nil
	},
)

// bigResultOp returns a large (offloaded) result.
var extStoreNexusBigResultOp = nexus.NewSyncOperation(
	"big-result-op",
	func(ctx context.Context, _ string, _ nexus.StartOperationOptions) (string, error) {
		return oversized(72), nil
	},
)

func extStoreNexusSizeCaller(ctx workflow.Context, endpoint string) (int, error) {
	c := workflow.NewNexusClient(endpoint, extStoreNexusServiceName)
	var res int
	err := c.ExecuteOperation(ctx, extStoreNexusSizeOp, oversized(72), workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 20 * time.Second,
	}).Get(ctx, &res)
	return res, err
}

func extStoreNexusBigResultCaller(ctx workflow.Context, endpoint string) (int, error) {
	c := workflow.NewNexusClient(endpoint, extStoreNexusServiceName)
	var res string
	err := c.ExecuteOperation(ctx, extStoreNexusBigResultOp, "small", workflow.NexusOperationOptions{
		ScheduleToCloseTimeout: 20 * time.Second,
	}).Get(ctx, &res)
	// Return only the length so the workflow's own result isn't offloaded; that keeps
	// the store/retrieve counts attributable to the Nexus operation result alone.
	return len(res), err
}

// transientFailDriver fails the first store and/or retrieve call, then delegates to
// the embedded in-memory driver. It simulates a transient storage-driver outage.
type transientFailDriver struct {
	*memStorageDriver
	mu                sync.Mutex
	failFirstStore    bool
	failFirstRetrieve bool
	storeAttempts     int
	retrieveAttempts  int
}

func (d *transientFailDriver) Store(ctx converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageDriverClaim, error) {
	d.mu.Lock()
	d.storeAttempts++
	fail := d.failFirstStore && d.storeAttempts == 1
	d.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("transient store failure")
	}
	return d.memStorageDriver.Store(ctx, payloads)
}

func (d *transientFailDriver) Retrieve(ctx converter.StorageDriverRetrieveContext, claims []converter.StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	d.retrieveAttempts++
	fail := d.failFirstRetrieve && d.retrieveAttempts == 1
	d.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("transient retrieve failure")
	}
	return d.memStorageDriver.Retrieve(ctx, claims)
}

func (d *transientFailDriver) attempts() (store, retrieve int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.storeAttempts, d.retrieveAttempts
}

// newNexusExtStoreClient dials a client with external storage configured and creates a
// Nexus endpoint targeting a fresh task queue, returning the client, task queue, and
// endpoint name.
func newNexusExtStoreClient(t *testing.T, ctx context.Context, driver converter.StorageDriver) (client.Client, string, string) {
	clientBase := ConfigAndClientSuiteBase{}
	clientBase.initConfig()
	config := clientBase.config
	require.NoError(t, WaitForTCP(time.Minute, config.ServiceAddr))
	c, err := clientBase.newDefaultClientContext(ctx, func(options *client.Options) {
		options.ExternalStorage = converter.ExternalStorage{
			Drivers:              []converter.StorageDriver{driver},
			PayloadSizeThreshold: extStoreThreshold,
		}
	})
	require.NoError(t, err)

	taskQueue := "sdk-go-nexus-ext-tq-" + uuid.NewString()
	endpoint := "sdk-go-nexus-ext-ep-" + uuid.NewString()
	_, err = c.OperatorService().CreateNexusEndpoint(ctx, &operatorservice.CreateNexusEndpointRequest{
		Spec: &nexuspb.EndpointSpec{
			Name: endpoint,
			Target: &nexuspb.EndpointTarget{
				Variant: &nexuspb.EndpointTarget_Worker_{
					Worker: &nexuspb.EndpointTarget_Worker{
						Namespace: config.Namespace,
						TaskQueue: taskQueue,
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return c, taskQueue, endpoint
}

func newNexusExtStoreWorker(t *testing.T, c client.Client, taskQueue string, callerWorkflow any) worker.Worker {
	w := worker.New(c, taskQueue, worker.Options{})
	service := nexus.NewService(extStoreNexusServiceName)
	require.NoError(t, service.Register(extStoreNexusSizeOp, extStoreNexusBigResultOp))
	w.RegisterNexusService(service)
	w.RegisterWorkflow(callerWorkflow)
	require.NoError(t, w.Start())
	return w
}

func startNexusExtStoreCaller(t *testing.T, ctx context.Context, c client.Client, taskQueue string, callerWorkflow any, endpoint string) client.WorkflowRun {
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue: taskQueue,
		// The endpoint registry may take a bit to propagate; a short workflow task
		// timeout speeds up retries.
		WorkflowTaskTimeout: time.Second,
	}, callerWorkflow, endpoint)
	require.NoError(t, err)
	return run
}

// TestNexusExternalStorageOperationInput verifies that an operation input offloaded to
// external storage by the caller is retrieved before the handler runs.
func TestNexusExternalStorageOperationInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := newMemDriver("test")
	c, taskQueue, endpoint := newNexusExtStoreClient(t, ctx, driver)
	defer c.Close()
	w := newNexusExtStoreWorker(t, c, taskQueue, extStoreNexusSizeCaller)
	defer w.Stop()

	run := startNexusExtStoreCaller(t, ctx, c, taskQueue, extStoreNexusSizeCaller, endpoint)
	var res int
	require.NoError(t, run.Get(ctx, &res))
	require.Equal(t, len(oversized(72)), res)

	store, retrieve := driver.getStoreCounts()
	require.Greater(t, store, 0, "caller should have offloaded the large input")
	require.Greater(t, retrieve, 0, "handler should have retrieved the offloaded input")
}

// TestNexusExternalStorageOperationResult verifies that a large synchronous result is
// offloaded when completing the task and retrieved by the caller.
func TestNexusExternalStorageOperationResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := newMemDriver("test")
	c, taskQueue, endpoint := newNexusExtStoreClient(t, ctx, driver)
	defer c.Close()
	w := newNexusExtStoreWorker(t, c, taskQueue, extStoreNexusBigResultCaller)
	defer w.Stop()

	run := startNexusExtStoreCaller(t, ctx, c, taskQueue, extStoreNexusBigResultCaller, endpoint)
	var res int
	require.NoError(t, run.Get(ctx, &res))
	require.Equal(t, len(oversized(72)), res)

	store, retrieve := driver.getStoreCounts()
	require.Greater(t, store, 0, "handler should have offloaded the large result")
	require.Greater(t, retrieve, 0, "caller should have retrieved the offloaded result")
}

// TestNexusExternalStorageTransientStoreFailureRecovers verifies that a transient
// storage failure while offloading the result fails the task retryably and then
// recovers on redelivery.
func TestNexusExternalStorageTransientStoreFailureRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	driver := &transientFailDriver{memStorageDriver: newMemDriver("test"), failFirstStore: true}
	c, taskQueue, endpoint := newNexusExtStoreClient(t, ctx, driver)
	defer c.Close()
	w := newNexusExtStoreWorker(t, c, taskQueue, extStoreNexusBigResultCaller)
	defer w.Stop()

	run := startNexusExtStoreCaller(t, ctx, c, taskQueue, extStoreNexusBigResultCaller, endpoint)
	var res int
	require.NoError(t, run.Get(ctx, &res))
	require.Equal(t, len(oversized(72)), res)

	store, _ := driver.attempts()
	require.GreaterOrEqual(t, store, 2, "store should have been retried after the transient failure")
}
