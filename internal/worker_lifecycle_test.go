// These tests run SDK workers against an in-process service and hold cleanup
// with channels. They verify poller retirement, caller results, and Stop joins
// without using elapsed time to release a blocked operation.
package internal

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	genenumspb "go.temporal.io/api/enums/v1"
	gennamespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/serviceerror"
	genworkflowservice "go.temporal.io/api/workflowservice/v1"
	genworkflowservicemock "go.temporal.io/api/workflowservicemock/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"google.golang.org/grpc"
)

type (
	// lifecycleWorker runs real SDK polling goroutines against an in-process
	// service. Its first poll delivers synthetic causes through the SDK's
	// installed fatal callback; cleanup waits for the test's release.
	lifecycleWorker struct {
		worker          *AggregatedWorker
		pollStarted     chan struct{}
		triggerFatal    func()
		shutdownEntered chan struct{}
		releaseShutdown func()
		shutdownCalls   atomic.Int32
	}

	// lifecycleStopPlugin holds cleanup after child workers stop, so tests can
	// verify that Stop also joins the rest of the plugin call.
	lifecycleStopPlugin struct {
		WorkerPluginBase
		afterNext chan struct{}
		release   chan struct{}
		calls     atomic.Int32
	}

	// lifecycleFatalPoller calls the installed SDK fatal callback directly from
	// PollTask. The SDK's fixed poller or autoscaling RPC goroutine owns that
	// call and remains counted until PollTask and its enclosing goroutine exit.
	lifecycleFatalPoller struct {
		worker  *lifecycleWorker
		cause   error
		trigger chan struct{}
		first   atomic.Bool
	}
)

func TestWorkerLifecycleCountedFatalProducerRetiresDuringCleanup(t *testing.T) {
	for _, autoscaling := range []bool{false, true} {
		for _, withHook := range []bool{false, true} {
			name := map[bool]string{false: "fixed", true: "autoscaling"}[autoscaling]
			name += map[bool]string{false: "/no_hook", true: "/hook_stop"}[withHook]
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					cause := serviceerror.NewNamespaceNotFound("synthetic-namespace")
					opts := lifecycleOptions(autoscaling, "activity")
					hostClosed := make(chan struct{})
					var f *lifecycleWorker
					if withHook {
						opts.OnFatalError = func(err error) {
							assert.Same(t, cause, err)
							f.worker.Stop()
							f.worker.client.Close()
							close(hostClosed)
						}
					}
					f = newLifecycleWorker(t, opts, nil)
					producer := &lifecycleFatalPoller{worker: f, cause: cause, trigger: make(chan struct{})}
					f.triggerFatal = sync.OnceFunc(func() { close(producer.trigger) })
					base := f.worker.activityWorker.worker
					// Rate pacing is separate from fatal callback scheduling. Keep
					// every synthetic poll immediately runnable in this proof.
					base.pollLimiter = nil
					// This raw base-worker test starts the actual SDK polling groups
					// with a synthetic poller, avoiding the uncounted service RPC
					// goroutine. Aggregate Stop still owns its real cleanup body.
					f.worker.memoizedStart = sync.OnceValue(func() error {
						base.options.taskPollers = []scalableTaskPoller{newScalableTaskPoller(
							producer, f.worker.logger, opts.ActivityTaskPollerBehavior,
							metrics.PollerTypeActivityTask, nil, nil,
						)}
						base.Start()
						return nil
					})
					require.NoError(t, f.worker.Start())
					<-f.pollStarted
					f.triggerFatal()
					<-f.shutdownEntered
					retired := lifecyclePollersRetired(f.worker)
					synctest.Wait()
					requireLifecycleClosed(t, retired, "counted producer did not retire while real cleanup was held")
					requireLifecyclePending(t, hostClosed, "hook closed the host before SDK cleanup")
					firstStop := lifecycleStopAsync(f.worker)
					secondStop := lifecycleStopAsync(f.worker)
					synctest.Wait()
					requireLifecyclePending(t, firstStop, "concurrent Stop skipped cleanup")
					requireLifecyclePending(t, secondStop, "second Stop skipped cleanup")
					f.releaseShutdown()
					<-firstStop
					<-secondStop
					if withHook {
						<-hostClosed
					}
					assert.EqualValues(t, 1, f.shutdownCalls.Load())
				})
			})
		}
	}
}

func TestWorkerLifecycleKindsJoinCleanup(t *testing.T) {
	for _, autoscaling := range []bool{false, true} {
		for _, kind := range []string{"activity", "workflow", "session", "nexus"} {
			for _, withHook := range []bool{false, true} {
				name := kind + "/fixed"
				if autoscaling {
					name = kind + "/autoscaling"
				}
				if withHook {
					name += "/hook"
				} else {
					name += "/no_hook"
				}
				t.Run(name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						cause := serviceerror.NewNamespaceNotFound("synthetic-namespace")
						opts := lifecycleOptions(autoscaling, kind)
						notification := make(chan error, 1)
						if withHook {
							opts.OnFatalError = func(err error) { notification <- err }
						}
						f := newLifecycleWorker(t, opts, []error{cause})
						require.NoError(t, f.worker.Start())
						<-f.pollStarted
						f.triggerFatal()
						<-f.shutdownEntered

						if withHook {
							require.Same(t, cause, <-notification)
						}
						done := lifecycleStopAsync(f.worker)
						synctest.Wait()
						requireLifecyclePending(t, done, "Stop skipped held SDK cleanup")
						f.releaseShutdown()
						<-done
						expectedRequests := 1
						if kind == "session" {
							expectedRequests = 3
						}
						assert.EqualValues(t, expectedRequests, f.shutdownCalls.Load())
					})
				})
			}
		}
	}
}

func TestWorkerLifecycleRunRetainsFatalWhenInterrupted(t *testing.T) {
	for _, bothReady := range []bool{false, true} {
		name := "interrupt_before_stop_request"
		if bothReady {
			name = "interrupt_and_stop_request_ready"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				first := serviceerror.NewNamespaceNotFound("first-namespace")
				later := serviceerror.NewPermissionDenied("later failure", "")
				hookEntered := make(chan struct{})
				releaseHook := make(chan struct{})
				unblockHook := sync.OnceFunc(func() { close(releaseHook) })
				defer unblockHook()
				var hookCalls atomic.Int32
				opts := lifecycleOptions(false, "activity")
				opts.OnFatalError = func(err error) {
					hookCalls.Add(1)
					assert.Same(t, first, err)
					close(hookEntered)
					<-releaseHook
				}
				f := newLifecycleWorker(t, opts, []error{first, later})
				require.NoError(t, f.worker.Start())
				interrupt := make(chan any)
				runEntered, enterSelect := lifecycleRunStartBarrier(f.worker)
				result := lifecycleRunAsync(f.worker, interrupt)
				<-runEntered
				f.triggerFatal()
				<-hookEntered
				close(interrupt)
				var externalStop <-chan struct{}
				if bothReady {
					externalStop = lifecycleStopAsync(f.worker)
					<-f.shutdownEntered
				}
				enterSelect()
				<-f.shutdownEntered
				synctest.Wait()
				select {
				case err := <-result:
					t.Fatalf("Run returned before cleanup finished: %v", err)
				default:
				}
				unblockHook()
				f.releaseShutdown()
				err := <-result
				assert.Same(t, first, err)
				var typed *serviceerror.NamespaceNotFound
				assert.ErrorAs(t, err, &typed)
				assert.Same(t, first, typed)
				assert.ErrorIs(t, err, first)
				assert.EqualValues(t, 1, hookCalls.Load())
				assert.EqualValues(t, 1, f.shutdownCalls.Load())
				if externalStop != nil {
					<-externalStop
				}
			})
		})
	}
}

func TestWorkerLifecycleRunAndStopJoinFullCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := serviceerror.NewNamespaceNotFound("synthetic-namespace")
		f := newLifecycleWorker(t, lifecycleOptions(true, "activity"), []error{cause})
		runEntered, enterSelect := lifecycleRunStartBarrier(f.worker)
		result := lifecycleRunAsync(f.worker, nil)
		<-runEntered
		enterSelect()
		f.triggerFatal()
		<-f.shutdownEntered
		firstStop := lifecycleStopAsync(f.worker)
		secondStop := lifecycleStopAsync(f.worker)
		synctest.Wait()
		requireLifecyclePending(t, firstStop, "first Stop returned during cleanup")
		requireLifecyclePending(t, secondStop, "second Stop returned during cleanup")
		select {
		case err := <-result:
			t.Fatalf("Run returned while ShutdownWorker was held: %v", err)
		default:
		}
		f.releaseShutdown()
		assert.Same(t, cause, <-result)
		<-firstStop
		<-secondStop
		assert.EqualValues(t, 1, f.shutdownCalls.Load())
	})
}

func TestWorkerLifecycleOrdinaryInterruptedRunJoinsCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newLifecycleWorker(t, lifecycleOptions(false, "activity"), nil)
		runEntered, enterSelect := lifecycleRunStartBarrier(f.worker)
		interrupt := make(chan any)
		result := lifecycleRunAsync(f.worker, interrupt)
		<-runEntered
		close(interrupt)
		enterSelect()
		<-f.shutdownEntered
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("ordinary interrupted Run returned before cleanup: %v", err)
		default:
		}
		f.releaseShutdown()
		assert.NoError(t, <-result)
		assert.EqualValues(t, 1, f.shutdownCalls.Load())
	})
}

func TestWorkerLifecycleHookCanStopAndCloseHost(t *testing.T) {
	for _, autoscaling := range []bool{false, true} {
		t.Run(map[bool]string{false: "fixed", true: "autoscaling"}[autoscaling], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := serviceerror.NewNamespaceNotFound("synthetic-namespace")
				hostClosed := make(chan struct{})
				var f *lifecycleWorker
				opts := lifecycleOptions(autoscaling, "activity")
				opts.OnFatalError = func(err error) {
					assert.Same(t, cause, err)
					f.worker.Stop()
					f.worker.client.Close()
					close(hostClosed)
				}
				f = newLifecycleWorker(t, opts, []error{cause})
				require.NoError(t, f.worker.Start())
				f.triggerFatal()
				<-f.shutdownEntered
				synctest.Wait()
				requireLifecyclePending(t, hostClosed, "host closed its client during SDK cleanup")
				f.releaseShutdown()
				<-hostClosed
				synctest.Wait()
				assert.EqualValues(t, 1, f.shutdownCalls.Load())
			})
		})
	}
}

func TestWorkerLifecycleOrdinaryStopCanPrecedeFatalNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := serviceerror.NewNamespaceNotFound("synthetic-namespace")
		hookEntered := make(chan struct{})
		releaseHook := make(chan struct{})
		unblockHook := sync.OnceFunc(func() { close(releaseHook) })
		defer unblockHook()
		delivered := make(chan error, 1)
		opts := lifecycleOptions(true, "activity")
		opts.OnFatalError = func(err error) {
			close(hookEntered)
			<-releaseHook
			delivered <- err
		}
		f := newLifecycleWorker(t, opts, []error{cause})
		runEntered, enterSelect := lifecycleRunStartBarrier(f.worker)
		result := lifecycleRunAsync(f.worker, nil)
		<-runEntered
		enterSelect()
		<-f.pollStarted
		stopped := lifecycleStopAsync(f.worker)
		<-f.shutdownEntered
		f.triggerFatal()
		<-hookEntered
		f.releaseShutdown()
		<-stopped
		assert.Same(t, cause, <-result)
		select {
		case <-delivered:
			t.Fatal("notification was delivered before the hook was released")
		default:
		}
		unblockHook()
		assert.Same(t, cause, <-delivered)
		assert.EqualValues(t, 1, f.shutdownCalls.Load())
	})
}

func TestWorkerLifecycleIndependentWorkersRetainTheirCauses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := serviceerror.NewNamespaceNotFound("first-namespace")
		second := serviceerror.NewPermissionDenied("second worker failure", "")
		a := newLifecycleWorker(t, lifecycleOptions(false, "activity"), []error{first})
		b := newLifecycleWorker(t, lifecycleOptions(true, "activity"), []error{second})
		aEntered, releaseA := lifecycleRunStartBarrier(a.worker)
		bEntered, releaseB := lifecycleRunStartBarrier(b.worker)
		aResult := lifecycleRunAsync(a.worker, nil)
		bResult := lifecycleRunAsync(b.worker, nil)
		<-aEntered
		<-bEntered
		releaseA()
		releaseB()
		a.triggerFatal()
		b.triggerFatal()
		<-a.shutdownEntered
		<-b.shutdownEntered
		a.releaseShutdown()
		assert.Same(t, first, <-aResult)
		synctest.Wait()
		select {
		case err := <-bResult:
			t.Fatalf("second worker stop did not remain independently held: %v", err)
		default:
		}
		b.releaseShutdown()
		secondResult := <-bResult
		assert.Same(t, second, secondResult)
		joined := errors.Join(first, secondResult)
		assert.ErrorIs(t, joined, first)
		assert.ErrorIs(t, joined, second)
		var namespaceErr *serviceerror.NamespaceNotFound
		var permissionErr *serviceerror.PermissionDenied
		assert.ErrorAs(t, joined, &namespaceErr)
		assert.ErrorAs(t, joined, &permissionErr)
		assert.Same(t, first, namespaceErr)
		assert.Same(t, second, permissionErr)
		assert.EqualValues(t, 1, a.shutdownCalls.Load())
		assert.EqualValues(t, 1, b.shutdownCalls.Load())
	})
}

func TestWorkerLifecycleStopJoinsPluginAfterChildCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		plugin := &lifecycleStopPlugin{
			afterNext: make(chan struct{}),
			release:   make(chan struct{}),
		}
		releasePlugin := sync.OnceFunc(func() { close(plugin.release) })
		defer releasePlugin()
		opts := lifecycleOptions(false, "activity")
		opts.Plugins = []WorkerPlugin{plugin}
		f := newLifecycleWorker(t, opts, nil)
		runEntered, enterSelect := lifecycleRunStartBarrier(f.worker)
		result := lifecycleRunAsync(f.worker, nil)
		<-runEntered
		enterSelect()
		firstStop := lifecycleStopAsync(f.worker)
		<-f.shutdownEntered
		f.releaseShutdown()
		<-plugin.afterNext
		secondStop := lifecycleStopAsync(f.worker)
		synctest.Wait()
		requireLifecyclePending(t, firstStop, "Stop omitted the remainder of its plugin")
		requireLifecyclePending(t, secondStop, "concurrent Stop skipped its plugin")
		select {
		case err := <-result:
			t.Fatalf("Run returned before its stop plugin returned: %v", err)
		default:
		}
		releasePlugin()
		<-firstStop
		<-secondStop
		assert.NoError(t, <-result)
		assert.EqualValues(t, 1, plugin.calls.Load())
		assert.EqualValues(t, 1, f.shutdownCalls.Load())
	})
}

func TestWorkerLifecycleStartFailureRetainsOriginalCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cause := serviceerror.NewNamespaceNotFound("startup-namespace")
		opts := lifecycleOptions(false, "activity")
		plugin := &startFailWorkerPlugin{startErr: cause}
		opts.Plugins = []WorkerPlugin{plugin}
		f := newLifecycleWorker(t, opts, nil)
		assert.Same(t, cause, f.worker.Run(nil))
		assert.EqualValues(t, 0, f.shutdownCalls.Load())
		f.releaseShutdown()
		f.worker.Stop()
		assert.EqualValues(t, 1, plugin.stopCalls.Load())
	})
}

// newLifecycleWorker starts no network service. Its SDK client calls typed
// service mocks, while SDK workers own all polling and shutdown goroutines.
func newLifecycleWorker(t *testing.T, opts WorkerOptions, causes []error) *lifecycleWorker {
	t.Helper()
	ctrl := gomock.NewController(t)
	service := genworkflowservicemock.NewMockWorkflowServiceClient(ctrl)
	service.EXPECT().GetSystemInfo(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.GetSystemInfoResponse{}, nil).AnyTimes()
	service.EXPECT().DescribeNamespace(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&genworkflowservice.DescribeNamespaceResponse{
			NamespaceInfo: &gennamespacepb.NamespaceInfo{
				Name:  "synthetic-namespace",
				State: genenumspb.NAMESPACE_STATE_REGISTERED,
			},
		}, nil).AnyTimes()
	trigger := make(chan struct{})
	release := make(chan struct{})
	f := &lifecycleWorker{
		pollStarted:     make(chan struct{}),
		triggerFatal:    sync.OnceFunc(func() { close(trigger) }),
		shutdownEntered: make(chan struct{}),
		releaseShutdown: sync.OnceFunc(func() { close(release) }),
	}
	var fatalProducer atomic.Bool
	poll := func(kind string) {
		if kind != opts.Identity {
			<-f.worker.stopC
			return
		}
		if fatalProducer.CompareAndSwap(false, true) {
			close(f.pollStarted)
			if len(causes) > 0 {
				<-trigger
				for _, err := range causes {
					f.worker.executionParams.WorkerFatalErrorCallback(err)
				}
			}
		}
		<-f.worker.stopC
	}
	service.EXPECT().PollActivityTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *genworkflowservice.PollActivityTaskQueueRequest, _ ...grpc.CallOption) (*genworkflowservice.PollActivityTaskQueueResponse, error) {
			kind := "activity"
			if opts.EnableSessionWorker && req.TaskQueue.Name != "synthetic-task-queue" {
				kind = "session"
			}
			poll(kind)
			return &genworkflowservice.PollActivityTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().PollWorkflowTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *genworkflowservice.PollWorkflowTaskQueueRequest, ...grpc.CallOption) (*genworkflowservice.PollWorkflowTaskQueueResponse, error) {
			poll("workflow")
			return &genworkflowservice.PollWorkflowTaskQueueResponse{}, nil
		}).AnyTimes()
	service.EXPECT().PollNexusTaskQueue(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *genworkflowservice.PollNexusTaskQueueRequest, ...grpc.CallOption) (*genworkflowservice.PollNexusTaskQueueResponse, error) {
			poll("nexus")
			return &genworkflowservice.PollNexusTaskQueueResponse{}, nil
		}).AnyTimes()
	var shutdownOnce sync.Once
	service.EXPECT().ShutdownWorker(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, *genworkflowservice.ShutdownWorkerRequest, ...grpc.CallOption) (*genworkflowservice.ShutdownWorkerResponse, error) {
			f.shutdownCalls.Add(1)
			shutdownOnce.Do(func() {
				close(f.shutdownEntered)
				<-release
			})
			return &genworkflowservice.ShutdownWorkerResponse{}, nil
		}).AnyTimes()
	cli := NewServiceClient(service, nil, ClientOptions{Namespace: "synthetic-namespace"})
	f.worker = NewAggregatedWorker(cli, "synthetic-task-queue", opts)
	f.worker.RegisterActivityWithOptions(
		func(context.Context) error { return nil },
		RegisterActivityOptions{Name: "SyntheticActivity"},
	)
	if !opts.DisableWorkflowWorker {
		f.worker.RegisterWorkflowWithOptions(
			func(Context) error { return nil },
			RegisterWorkflowOptions{Name: "SyntheticWorkflow"},
		)
	}
	if opts.Identity == "nexus" {
		svc := nexus.NewService("SyntheticService")
		require.NoError(t, svc.Register(nexus.NewSyncOperation(
			"operation", func(context.Context, string, nexus.StartOperationOptions) (string, error) {
				return "result", nil
			},
		)))
		f.worker.RegisterNexusService(svc)
	}
	t.Cleanup(func() {
		f.triggerFatal()
		f.releaseShutdown()
		f.worker.Stop()
		cli.Close()
	})
	return f
}

func lifecycleOptions(autoscaling bool, kind string) WorkerOptions {
	var behavior PollerBehavior = NewPollerBehaviorSimpleMaximum(
		PollerBehaviorSimpleMaximumOptions{MaximumNumberOfPollers: 2},
	)
	if autoscaling {
		behavior = NewPollerBehaviorAutoscaling(PollerBehaviorAutoscalingOptions{
			InitialNumberOfPollers: 2,
			MinimumNumberOfPollers: 2,
			MaximumNumberOfPollers: 2,
		})
	}
	return WorkerOptions{
		Identity:                   kind,
		DisableWorkflowWorker:      kind != "workflow",
		EnableSessionWorker:        kind == "session",
		ActivityTaskPollerBehavior: behavior,
		WorkflowTaskPollerBehavior: behavior,
		NexusTaskPollerBehavior:    behavior,
		WorkerStopTimeout:          time.Minute,
	}
}

// lifecyclePollersRetired waits for SDK poller groups, including autoscaling
// managers and their child poll RPCs, so a PollTask return alone cannot pass.
func lifecyclePollersRetired(w *AggregatedWorker) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		if w.workflowWorker != nil {
			w.workflowWorker.worker.pollerWG.Wait()
		}
		if w.activityWorker != nil {
			w.activityWorker.worker.pollerWG.Wait()
		}
		if w.sessionWorker != nil {
			w.sessionWorker.activityWorker.worker.pollerWG.Wait()
			w.sessionWorker.creationWorker.worker.pollerWG.Wait()
		}
		if w.nexusWorker != nil {
			w.nexusWorker.worker.pollerWG.Wait()
		}
		close(done)
	}()
	return done
}

// lifecycleRunStartBarrier pauses Run after the real memoized Start succeeds.
// Tests can make interruption and stop requests ready before Run selects one.
func lifecycleRunStartBarrier(w *AggregatedWorker) (<-chan struct{}, func()) {
	start := w.memoizedStart
	entered := make(chan struct{})
	release := make(chan struct{})
	w.memoizedStart = func() error {
		err := start()
		close(entered)
		<-release
		return err
	}
	return entered, sync.OnceFunc(func() { close(release) })
}

func lifecycleRunAsync(w *AggregatedWorker, interrupt <-chan any) <-chan error {
	result := make(chan error, 1)
	go func() { result <- w.Run(interrupt) }()
	return result
}

func lifecycleStopAsync(w *AggregatedWorker) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()
	return done
}

func requireLifecycleClosed(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	default:
		t.Fatal(message)
	}
}

func requireLifecyclePending(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal(message)
	default:
	}
}

func (p *lifecycleStopPlugin) Name() string {
	return "synthetic-stop-plugin"
}

func (p *lifecycleStopPlugin) StopWorker(ctx context.Context, opts WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
	p.calls.Add(1)
	next(ctx, opts)
	close(p.afterNext)
	<-p.release
}

func (p *lifecycleFatalPoller) PollTask(pollerGroupLease) (taskForWorker, error) {
	if p.first.CompareAndSwap(false, true) {
		close(p.worker.pollStarted)
		<-p.trigger
		p.worker.worker.executionParams.WorkerFatalErrorCallback(p.cause)
	}
	<-p.worker.worker.stopC
	return nil, nil
}
