// These tests call Start, Run, and Stop on workers backed by typed service mocks.
// They hold or call Stop from real startup hooks, then check context cancellation,
// callback order, and rejection of resources added after cleanup has finished.
package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gentaskqueuepb "go.temporal.io/api/taskqueue/v1"
)

// When a startup hook is held or calls Stop, cleanup finishes independently.
// A real later resource change rejects startup with ErrWorkerShutdown.
func TestWorkerStartupAdmissionCallbacks(t *testing.T) {
	previous := enableVerboseLogging
	EnableVerboseLogging(true)
	defer EnableVerboseLogging(previous)
	events := []string{
		"workflow.max_slots",
		"LocalActivityWorker.counter", "LocalActivityWorker.inc",
		"WorkflowWorker.counter", "WorkflowWorker.inc",
		"ActivityWorker.1.counter", "ActivityWorker.1.inc",
		"session.info",
		"ActivityWorker.2.counter", "ActivityWorker.2.inc",
		"ActivityWorker.3.counter", "ActivityWorker.3.inc",
		"nexus.poller.tags", "nexus.poller.gauge", "nexus.getter",
		"nexus.logger.with", "nexus.logger.skip", "nexus.worker.tags",
		"nexus.available.gauge", "nexus.used.gauge",
		"NexusWorker.counter", "NexusWorker.inc",
		"LocalActivityWorker.started", "WorkflowWorker.started",
		"ActivityWorker.1.started", "ActivityWorker.2.started",
		"ActivityWorker.3.started", "NexusWorker.started",
	}
	for _, event := range events {
		for _, synchronousStop := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stop_in_callback_%t", event, synchronousStop), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					hooks := &startupHooks{skipOnly: event == "nexus.logger.skip"}
					clientOpts := ClientOptions{}
					if event == "NexusWorker.started" {
						clientOpts.WorkerHeartbeatInterval = time.Second
					}
					f := newStartupFixture(t, startupOptions(false, true), clientOpts, hooks)
					f.heartbeats = event == "NexusWorker.started"
					f.worker.RegisterNexusService(startupService(t))
					gate := newStartupGate(t)
					var once sync.Once
					hooks.onEvent = func(seen string) {
						if seen == event {
							once.Do(func() {
								if synchronousStop {
									f.worker.Stop()
								}
								gate.block()
							})
						}
					}
					hooks.armed.Store(true)
					result := startupStartAsync(f.worker)
					<-gate.entered
					stopped := lifecycleStopAsync(f.worker)
					<-stopped
					synctest.Wait()
					startupRequireNoResult(t, result)
					// stopDone makes the child set immutable even though this
					// synchronous callback has not returned to Start yet.
					startupAssertCanceled(t, f.worker)
					if strings.HasPrefix(event, "nexus.") {
						assert.Nil(t, f.worker.nexusWorker)
					}
					gate.open()
					assert.ErrorIs(t, <-result, ErrWorkerShutdown)
					startupAssertCanceled(t, f.worker)
					startupAssertNoMembership(t, f.worker)
				})
			})
		}
	}
}

// After the last resource change, a finite logger can Stop the worker and
// return. Start and an attaching Run still return successful initialization.
func TestWorkerStartupAdmissionFinalLoggerStop(t *testing.T) {
	previous := enableVerboseLogging
	EnableVerboseLogging(false)
	defer EnableVerboseLogging(previous)
	for _, event := range []string{"aggregate.started", "heartbeat.unsupported"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hooks := &startupHooks{}
				clientOpts := ClientOptions{WorkerHeartbeatInterval: -1}
				if event == "heartbeat.unsupported" {
					clientOpts.WorkerHeartbeatInterval = time.Second
				}
				f := newStartupFixture(t, startupOptions(false, false), clientOpts, hooks)
				var stops atomic.Int32
				hooks.onEvent = func(seen string) {
					if seen == event {
						stops.Add(1)
						f.worker.Stop()
					}
				}
				hooks.armed.Store(true)
				require.NoError(t, f.worker.Start())
				requireLifecycleClosed(t, f.worker.stopDone, "the logger's Stop did not finish")
				require.NoError(t, f.worker.Run(nil))
				assert.EqualValues(t, 1, stops.Load())
				assert.Zero(t, f.tuner.getters.Load())
				startupAssertNoMembership(t, f.worker)
				require.PanicsWithValue(t, "attempted to start a worker that has been stopped before", func() {
					err := f.worker.Start()
					t.Errorf("Start after Stop returned %v", err)
				})
			})
		})
	}
	for _, kind := range []string{"activity", "nexus"} {
		t.Run("terminal_child_"+kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				EnableVerboseLogging(true)
				defer EnableVerboseLogging(false)
				hooks := &startupHooks{}
				f := newStartupFixture(t, startupOptions(true, false), ClientOptions{}, hooks)
				event := "ActivityWorker.1.started"
				if kind == "nexus" {
					f.worker.RegisterNexusService(startupService(t))
					event = "NexusWorker.started"
				}
				hooks.onEvent = func(seen string) {
					if seen == event {
						f.worker.Stop()
					}
				}
				hooks.armed.Store(true)
				// This child log follows the last required mutation. Stop
				// cleans it, and the original startup call still succeeds.
				require.NoError(t, f.worker.Start())
				require.NoError(t, f.worker.Run(nil))
				startupAssertCanceled(t, f.worker)
				startupAssertNoMembership(t, f.worker)
			})
		})
	}
	t.Run("ordinary_interrupt", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStartupFixture(t, startupOptions(false, false), ClientOptions{}, nil)
			interrupt := make(chan any)
			result := lifecycleRunAsync(f.worker, interrupt)
			<-f.pollStarted
			close(interrupt)
			assert.NoError(t, <-result)
			requireLifecycleClosed(t, f.worker.stopDone, "Run returned before cleanup")
		})
	})
}

// Plugins can register Nexus services before SDK startup. Validation stays
// after earlier polling; successful preparation preserves hooks and suppliers.
func TestWorkerStartupAdmissionLateNexusOrder(t *testing.T) {
	for _, kind := range []string{"absent", "invalid", "worker_plugin", "client_plugin", "nil", "typed_nil"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hooks := &startupHooks{}
				opts := startupOptions(false, false)
				clientOpts := ClientOptions{}
				plugin := &startupPlugin{}
				var f *startupFixture
				if strings.HasSuffix(kind, "_plugin") {
					service := startupService(t)
					replacement, err := NewFixedSizeSlotSupplier(3)
					require.NoError(t, err)
					plugin.start = func(ctx context.Context, in WorkerPluginStartWorkerOptions, next func(context.Context, WorkerPluginStartWorkerOptions) error) error {
						in.WorkerRegistry.RegisterNexusService(service)
						f.tuner.nexus = replacement
						return next(ctx, in)
					}
					if kind == "client_plugin" {
						clientOpts.Plugins = []ClientPlugin{plugin}
					} else {
						opts.Plugins = []WorkerPlugin{plugin}
					}
				}
				f = newStartupFixture(t, opts, clientOpts, hooks)
				if kind == "invalid" {
					f.worker.RegisterNexusService(nexus.NewService("EmptyService"))
				} else if kind == "nil" || kind == "typed_nil" {
					f.worker.RegisterNexusService(startupService(t))
					if kind == "nil" {
						f.tuner.nexus = nil
					} else {
						var supplier *FixedSizeSlotSupplier
						f.tuner.nexus = supplier
					}
				}
				var orderGate *startupGate
				if kind != "absent" {
					orderGate = newStartupGate(t)
					// Holding the ordinary activity counter until its real
					// workflow sibling polls proves validation remains late.
					hooks.onEvent = func(event string) {
						if event == "ActivityWorker.1.counter" {
							<-f.pollStarted
						}
						if event == "NexusWorker.counter" {
							orderGate.block()
						}
					}
				}
				hooks.armed.Store(true)
				result := startupStartAsync(f.worker)
				if strings.HasSuffix(kind, "_plugin") || kind == "nil" || kind == "typed_nil" {
					<-orderGate.entered
					require.NotNil(t, f.worker.nexusWorker)
					if kind == "nil" || kind == "typed_nil" {
						assert.Equal(t, f.tuner.nexus, f.worker.nexusWorker.worker.slotSupplier.inner)
					} else {
						assert.Same(t, f.tuner.nexus, f.worker.nexusWorker.worker.slotSupplier.inner)
					}
					assert.EqualValues(t, 1, f.tuner.getters.Load())
					assert.Equal(t, []string{
						"nexus.poller.tags", "nexus.poller.gauge", "nexus.getter",
						"nexus.logger.with", "nexus.worker.tags",
						"nexus.available.gauge", "nexus.used.gauge", "NexusWorker.counter",
					}, hooks.nexusEvents())
					f.worker.Stop()
					orderGate.open()
					assert.ErrorIs(t, <-result, ErrWorkerShutdown)
				} else {
					err := <-result
					if kind == "invalid" {
						assert.ErrorContains(t, err, "failed to create a nexus worker")
						assert.False(t, errors.Is(err, ErrWorkerShutdown))
						requireLifecycleClosed(t, f.pollStarted, "Nexus validation moved before earlier polling")
					} else {
						assert.NoError(t, err)
					}
					assert.Zero(t, f.tuner.getters.Load())
					assert.Nil(t, f.worker.nexusWorker)
					f.worker.Stop()
				}
			})
		})
	}
}

// Stop cancels constructed children even without a launch. For a launched
// child, accepted task processing finishes before its task context is canceled.
func TestWorkerStartupAdmissionConstructedContexts(t *testing.T) {
	for _, kind := range []string{"never_attempted", "before_core_error", "nexus_unlaunched", "activity_disabled", "heartbeat_unstarted"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				opts := startupOptions(false, true)
				cause := errors.New("before SDK startup")
				if kind == "before_core_error" {
					opts.Plugins = []WorkerPlugin{&startupPlugin{
						start: func(context.Context, WorkerPluginStartWorkerOptions, func(context.Context, WorkerPluginStartWorkerOptions) error) error {
							return cause
						},
					}}
				}
				if kind == "activity_disabled" {
					opts.EnableSessionWorker = false
					opts.LocalActivityWorkerOnly = true
				}
				clientOpts := ClientOptions{}
				if kind == "heartbeat_unstarted" {
					clientOpts.WorkerHeartbeatInterval = time.Second
				}
				hooks := &startupHooks{}
				f := newStartupFixture(t, opts, clientOpts, hooks)
				contexts := startupContexts(f.worker)
				parent := f.worker.executionParams.BackgroundContext
				var heartbeat context.Context
				if kind == "heartbeat_unstarted" {
					heartbeat = f.client.heartbeatManager.sharedNamespaceWorkerFor(f.client.namespace).workerCtx
				}
				if kind == "nexus_unlaunched" {
					f.worker.RegisterNexusService(startupService(t))
					hooks.onEvent = func(event string) {
						if event == "NexusWorker.counter" {
							assert.False(t, f.worker.nexusWorker.worker.isWorkerStarted)
							f.worker.Stop()
						}
					}
					hooks.armed.Store(true)
					assert.ErrorIs(t, f.worker.Start(), ErrWorkerShutdown)
					contexts = append(contexts, startupContexts(f.worker)...)
				} else if kind == "before_core_error" {
					assert.Same(t, cause, f.worker.Start())
					requireLifecyclePending(t, f.worker.stopDone, "manual Start cleaned up a plugin error")
				}
				f.worker.Stop()
				for _, ctx := range contexts {
					requireLifecycleClosed(t, ctx.Done(), "owned child context survived Stop")
				}
				requireLifecycleClosed(t, parent.Done(), "aggregate background context survived Stop")
				assert.ErrorIs(t, context.Cause(parent), ErrWorkerShutdown)
				if heartbeat != nil {
					requireLifecycleClosed(t, heartbeat.Done(), "unstarted shared heartbeat context survived Stop")
				}
			})
		})
	}
	t.Run("accepted_task_drains", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStartupFixture(t, startupOptions(true, false), ClientOptions{}, nil)
			processing := newStartupGate(t)
			base := f.worker.activityWorker.worker
			f.worker.activityWorker.poller = &startupTaskPoller{stop: f.worker.stopC}
			base.options.taskProcessor = &startupTaskProcessor{processing: processing}
			require.NoError(t, f.worker.Start())
			<-processing.entered
			stopped := lifecycleStopAsync(f.worker)
			<-base.stopCh
			synctest.Wait()
			requireLifecyclePending(t, base.taskLimiterContext.Done(), "Stop canceled an accepted task before drain")
			requireLifecyclePending(t, stopped, "Stop returned with processing held")
			processing.open()
			<-stopped
			requireLifecycleClosed(t, base.taskLimiterContext.Done(), "drained task context was not canceled")
		})
	})
}

// Actual SDK mutexes hold admitted changes while Stop competes. Cleanup owns
// the complete change; rejected later changes cannot publish or launch a child.
func TestWorkerStartupAdmissionMutationsAgainstStop(t *testing.T) {
	previous := enableVerboseLogging
	EnableVerboseLogging(true)
	defer EnableVerboseLogging(previous)
	for _, kind := range []string{"settings", "eager", "nexus", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			opts := startupOptions(kind != "eager", false)
			hooks := &startupHooks{}
			clientOpts := ClientOptions{}
			if kind == "heartbeat" {
				clientOpts.WorkerHeartbeatInterval = time.Second
			}
			f := newStartupFixture(t, opts, clientOpts, hooks)
			f.groups = testPollerGroupsInfo(1, []*gentaskqueuepb.PollerGroupInfo{{Id: "startup-group", Weight: 1}})
			ready := make(chan struct{})
			var release func()
			var shared *sharedNamespaceWorker
			switch kind {
			case "settings":
				store := f.worker.executionParams.pollerGroupSnapshotStore
				f.describe = func() {
					store.mu.Lock()
					close(ready)
				}
				release = sync.OnceFunc(store.mu.Unlock)
			case "eager":
				release = sync.OnceFunc(f.client.eagerDispatcher.lock.Unlock)
				hooks.onEvent = func(event string) {
					if event == "WorkflowWorker.started" {
						f.client.eagerDispatcher.lock.Lock()
						close(ready)
					}
				}
			case "nexus":
				require.Empty(t, f.worker.executionParams.WorkerBuildID, "an explicit build ID bypasses the checksum barrier")
				f.worker.RegisterNexusService(startupService(t))
				release = sync.OnceFunc(binaryChecksumLock.Unlock)
				hooks.onEvent = func(event string) {
					if event == "nexus.used.gauge" {
						binaryChecksumLock.Lock()
						close(ready)
					}
				}
			case "heartbeat":
				f.heartbeats = true
				shared = f.client.heartbeatManager.sharedNamespaceWorkerFor(f.client.namespace)
				release = sync.OnceFunc(shared.pollerGroups.groupStore.mu.Unlock)
				hooks.onEvent = func(event string) {
					if event == "ActivityWorker.1.started" {
						shared.pollerGroups.groupStore.mu.Lock()
						close(ready)
					}
				}
			}
			// The signal precedes exactly one remaining SDK operation that
			// can acquire lifecycleMu. No Stop or attaching Run exists yet.
			hooks.armed.Store(true)
			result := startupStartAsync(f.worker)
			startupAwaitSignal(t, ready)
			defer release()
			startupAwaitLifecycleLock(t, f.worker)
			stopped := lifecycleStopAsync(f.worker)
			requireLifecyclePending(t, f.worker.stopC, "Stop sealed while startup owned the resource mutex")
			release()
			startupAwaitSignal(t, stopped)
			err := startupAwaitResult(t, result)
			assert.True(t, err == nil || errors.Is(err, ErrWorkerShutdown), "unexpected startup result: %v", err)
			startupAssertCanceled(t, f.worker)
			startupAssertNoMembership(t, f.worker)
			switch kind {
			case "settings":
				assert.EqualValues(t, 1, f.worker.executionParams.pollerGroupSnapshotStore.snapshot().version)
			case "nexus":
				require.NotNil(t, f.worker.nexusWorker, "Stop missed an admitted complete Nexus child")
				assert.Same(t, f.tuner.nexus, f.worker.nexusWorker.worker.slotSupplier.inner)
			case "heartbeat":
				assert.EqualValues(t, 1, shared.pollerGroups.groupStore.snapshot().version)
				requireLifecycleClosed(t, shared.workerCtx.Done(), "Stop missed admitted heartbeat state")
			}
		})
	}
	for _, event := range []string{"workflow.max_slots", "LocalActivityWorker.counter", "WorkflowWorker.counter", "ActivityWorker.1.counter", "session.info", "ActivityWorker.2.counter", "ActivityWorker.3.counter"} {
		for _, contendStop := range []bool{false, true} {
			t.Run(fmt.Sprintf("pure/%s/contend_stop_%t", event, contendStop), func(t *testing.T) {
				hooks := &startupHooks{}
				f := newStartupFixture(t, startupOptions(false, true), ClientOptions{}, hooks)
				gate := newStartupGate(t)
				var once sync.Once
				hooks.onEvent = func(seen string) {
					if seen == event {
						once.Do(gate.block)
					}
				}
				hooks.armed.Store(true)
				result := startupStartAsync(f.worker)
				startupAwaitSignal(t, gate.entered)
				// There is no callback inside these constructors or launches.
				// Contend at their actual mutex, without claiming an inner
				// pause. A completed public Start proves the startup winner.
				if contendStop {
					f.worker.lifecycleMu.Lock()
					gate.open()
					stopped := lifecycleStopAsync(f.worker)
					f.worker.lifecycleMu.Unlock()
					startupAwaitSignal(t, stopped)
					err := startupAwaitResult(t, result)
					assert.True(t, err == nil || errors.Is(err, ErrWorkerShutdown), "unexpected contention result: %v", err)
				} else {
					gate.open()
					require.NoError(t, startupAwaitResult(t, result))
					f.worker.Stop()
				}
				startupAssertCanceled(t, f.worker)
				startupAssertNoMembership(t, f.worker)
			})
		}
	}
}

// Startup waiting for the heartbeat manager does not hold Stop's worker mutex.
// Stop seals and reaches its RPC, then completes after manager removal can run.
func TestWorkerStartupAdmissionHeartbeatLockOrder(t *testing.T) {
	t.Run("manager_wait_does_not_hold_worker_mutex", func(t *testing.T) {
		previous := enableVerboseLogging
		EnableVerboseLogging(true)
		defer EnableVerboseLogging(previous)
		hooks := &startupHooks{}
		f := newStartupFixture(t, startupOptions(true, false), ClientOptions{WorkerHeartbeatInterval: time.Second}, hooks)
		f.heartbeats = true
		f.shutdown = newStartupGate(t)
		atLastLog := newStartupGate(t)
		hooks.onEvent = func(event string) {
			if event == "ActivityWorker.1.started" {
				atLastLog.block()
			}
		}
		manager := f.client.heartbeatManager
		manager.workersMutex.Lock()
		releaseManager := sync.OnceFunc(manager.workersMutex.Unlock)
		defer releaseManager()
		hooks.armed.Store(true)
		result := startupStartAsync(f.worker)
		startupAwaitSignal(t, atLastLog.entered)
		atLastLog.open()
		startupAwaitManagerWait(t)
		stopped := lifecycleStopAsync(f.worker)
		startupAwaitSignal(t, f.shutdown.entered)
		requireLifecycleClosed(t, f.worker.stopC, "waiting for the manager prevented Stop from sealing")
		f.shutdown.open()
		requireLifecyclePending(t, stopped, "Stop returned while its manager removal was blocked")
		releaseManager()
		assert.ErrorIs(t, startupAwaitResult(t, result), ErrWorkerShutdown)
		startupAwaitSignal(t, stopped)
		startupAssertNoMembership(t, f.worker)
	})
	t.Run("two_members_then_last_removal", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStartupFixture(t, startupOptions(true, false), ClientOptions{WorkerHeartbeatInterval: time.Second}, nil)
			f.heartbeats = true
			second := NewAggregatedWorker(f.client, "second-startup-queue", startupOptions(true, false))
			second.RegisterActivityWithOptions(func(context.Context) error { return nil }, RegisterActivityOptions{Name: "SecondActivity"})
			t.Cleanup(second.Stop)
			require.NoError(t, f.worker.Start())
			require.NoError(t, second.Start())
			manager := f.client.heartbeatManager
			manager.workersMutex.Lock()
			shared := manager.workers[f.client.namespace]
			shared.callbacksMutex.RLock()
			assert.Len(t, shared.callbacks, 2)
			shared.callbacksMutex.RUnlock()
			manager.workersMutex.Unlock()
			second.Stop()
			manager.workersMutex.Lock()
			assert.Same(t, shared, manager.workers[f.client.namespace])
			shared.callbacksMutex.RLock()
			assert.Len(t, shared.callbacks, 1)
			shared.callbacksMutex.RUnlock()
			manager.workersMutex.Unlock()
			requireLifecyclePending(t, shared.workerCtx.Done(), "removing one worker canceled the remaining member")
			f.worker.Stop()
			requireLifecycleClosed(t, shared.workerCtx.Done(), "last removal did not cancel the shared worker")
			requireLifecycleClosed(t, shared.stoppedC, "last removal did not join the launched heartbeat loop")
			manager.workersMutex.Lock()
			assert.Empty(t, manager.workers)
			manager.workersMutex.Unlock()
		})
	})
}
