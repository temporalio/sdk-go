// These tests call Start and Run while polling errors or startup plugins are held.
// Real polling loops classify service errors after the existing retry period.
// Normal Run results wait for full Stop cleanup and retain the original errors;
// synchronous startup panics reach callers unchanged.
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"

	"go.temporal.io/sdk/internal/common/metrics"
)

// A poll returns a namespace error while Nexus configuration is held. The test
// uses actual time because Run's mutex wait cannot advance the simulated clock.
// Every polling goroutine finishes, and Run waits for cleanup and keeps the error.
func TestWorkerStartupAdmissionActualFatalDuringPreparation(t *testing.T) {
	// Each case owns its worker and waits for real retry timers. Run the cases
	// together after serial tests finish, then wait for every child's cleanup.
	t.Parallel()
	var cases sync.WaitGroup
	for _, autoscaling := range []bool{false, true} {
		for _, mode := range []string{"no_hook", "hook_stop", "delayed_hook", "second_classified_cause"} {
			cases.Go(func() {
				t.Run(fmt.Sprintf("autoscaling_%t/%s", autoscaling, mode), func(t *testing.T) {
					cause := serviceerror.NewNamespaceNotFound("fatal-startup-namespace")
					opts := startupOptions(true, false)
					if autoscaling {
						opts.ActivityTaskPollerBehavior = NewPollerBehaviorAutoscaling(PollerBehaviorAutoscalingOptions{
							InitialNumberOfPollers: 2, MinimumNumberOfPollers: 2, MaximumNumberOfPollers: 2,
						})
					}
					hook := newStartupGate(t)
					var hookCalls atomic.Int32
					var f *startupFixture
					switch mode {
					case "hook_stop":
						opts.OnFatalError = func(err error) {
							hookCalls.Add(1)
							assert.Same(t, cause, err)
							f.worker.Stop()
							hook.block()
						}
					case "delayed_hook", "second_classified_cause":
						opts.OnFatalError = func(err error) {
							hookCalls.Add(1)
							assert.Same(t, cause, err)
							hook.block()
						}
					}
					stopPlugin := &startupPlugin{}
					opts.Plugins = []WorkerPlugin{stopPlugin}
					hooks := &startupHooks{}
					f = newStartupFixture(t, opts, ClientOptions{}, hooks)
					afterStopNext := newStartupGate(t)
					stopPlugin.stop = func(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
						next(ctx, in)
						afterStopNext.block()
					}
					f.shutdown = newStartupGate(t)
					getter := newStartupGate(t)
					f.worker.RegisterNexusService(startupService(t))
					producer := &startupFatalPoller{
						worker: f.worker, cause: cause, started: make(chan struct{}), trigger: newStartupGate(t),
					}
					later := serviceerror.NewInvalidArgument("second classified poll failure")
					if mode == "second_classified_cause" {
						producer.second = newStartupGate(t)
						producer.secondCause = later
					}
					f.worker.activityWorker.poller = producer
					hooks.onEvent = func(event string) {
						if event == "nexus.getter" {
							// Polling must enter before the getter reports its
							// held state, without depending on global verbose logs.
							<-producer.started
							if producer.second != nil {
								// Both requests must already be issued before
								// the first error disables further polling.
								<-producer.second.entered
							}
							getter.block()
						}
					}
					classified := make(chan struct{})
					var laterOnce sync.Once
					var elapsed atomic.Int64
					hooks.onFatal = func(err error) {
						if err == cause {
							elapsed.CompareAndSwap(0, int64(f.worker.activityWorker.worker.retrier.GetElapsedTime()))
						}
						if err == later {
							laterOnce.Do(func() { close(classified) })
						}
					}
					hooks.armed.Store(true)
					startResult := startupStartAsync(f.worker)
					startupAwaitSignal(t, getter.entered)
					startupAwaitSignal(t, producer.started)
					runActor, runResult := startupRunAsync(t, f.worker)
					startupAwaitRunAttemptWait(t, runActor)
					producer.trigger.open()
					if mode == "delayed_hook" || mode == "second_classified_cause" {
						startupAwaitSignal(t, hook.entered)
						assert.Same(t, cause, startupFatalCause(f.worker))
						if mode == "second_classified_cause" {
							producer.second.open()
							startupAwaitSignal(t, classified)
						}
					} else {
						// Automatic Stop reaches this RPC only after the
						// poller saved the fatal cause. An earlier Stop would
						// cancel the producer before the retry grace elapsed.
						startupAwaitSignal(t, f.shutdown.entered)
					}
					stopped := lifecycleStopAsync(f.worker)
					startupAwaitSignal(t, f.shutdown.entered)
					assert.Same(t, cause, startupFatalCause(f.worker))
					assert.Greater(t, time.Duration(elapsed.Load()), getRetryLongPollGracePeriod())
					retired := make(chan struct{})
					go func() {
						f.worker.activityWorker.worker.pollerWG.Wait()
						close(retired)
					}()
					startupAwaitSignal(t, retired)
					// All polling goroutines finished while the shutdown
					// RPC is held. StopWorker plugins must still finish.
					startupRequireNoResult(t, startResult)
					startupRequireNoResult(t, runResult)
					requireLifecyclePending(t, stopped, "Stop skipped the real shutdown RPC")
					f.shutdown.open()
					startupAwaitSignal(t, afterStopNext.entered)
					startupRequireNoResult(t, runResult)
					requireLifecyclePending(t, f.worker.stopDone, "cleanup skipped after-next StopWorker")
					afterStopNext.open()
					startupAwaitSignal(t, stopped)
					if mode == "hook_stop" {
						startupAwaitSignal(t, hook.entered)
					}
					assert.Nil(t, f.worker.nexusWorker)
					startupAssertNoMembership(t, f.worker)
					startupRequireNoResult(t, startResult)
					startupRequireNoResult(t, runResult)
					getter.open()
					assert.ErrorIs(t, startupAwaitResult(t, startResult), ErrWorkerShutdown)
					err := startupAwaitResult(t, runResult)
					assert.ErrorIs(t, err, ErrWorkerShutdown)
					assert.ErrorIs(t, err, cause)
					var typed *serviceerror.NamespaceNotFound
					assert.ErrorAs(t, err, &typed)
					assert.Same(t, cause, typed)
					assert.Same(t, cause, startupFatalCause(f.worker))
					if mode == "delayed_hook" || mode == "second_classified_cause" {
						requireLifecyclePending(t, hook.release, "notification was released before the normal Run result")
					}
					hook.open()
					if mode != "no_hook" {
						assert.EqualValues(t, 1, hookCalls.Load())
					}
					assert.EqualValues(t, 1, f.shutdownCalls.Load())
					startupAssertCanceled(t, f.worker)
				})
			})
		}
	}
	cases.Wait()
}

// With no concurrent Run waiting on Start's mutex, the simulated clock advances
// actual polling errors through the existing grace. Stop finishes while the
// getter is held; a later Run receives the completed startup and fatal causes.
func TestWorkerStartupAdmissionFatalClassificationAndCleanup(t *testing.T) {
	previous := enableVerboseLogging
	EnableVerboseLogging(true)
	defer EnableVerboseLogging(previous)
	for _, autoscaling := range []bool{false, true} {
		t.Run(fmt.Sprintf("autoscaling_%t", autoscaling), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := serviceerror.NewNamespaceNotFound("fatal-startup-namespace")
				opts := startupOptions(true, false)
				if autoscaling {
					opts.ActivityTaskPollerBehavior = NewPollerBehaviorAutoscaling(PollerBehaviorAutoscalingOptions{
						InitialNumberOfPollers: 2, MinimumNumberOfPollers: 2, MaximumNumberOfPollers: 2,
					})
				}
				stopPlugin := &startupPlugin{}
				opts.Plugins = []WorkerPlugin{stopPlugin}
				hooks := &startupHooks{}
				f := newStartupFixture(t, opts, ClientOptions{}, hooks)
				afterStopNext := newStartupGate(t)
				stopPlugin.stop = func(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
					next(ctx, in)
					afterStopNext.block()
				}
				f.shutdown = newStartupGate(t)
				getter := newStartupGate(t)
				f.worker.RegisterNexusService(startupService(t))
				producer := &startupFatalPoller{
					worker: f.worker, cause: cause, started: make(chan struct{}), trigger: newStartupGate(t),
				}
				f.worker.activityWorker.poller = producer
				hooks.onEvent = func(event string) {
					if event == "ActivityWorker.1.started" {
						<-producer.started
					}
					if event == "nexus.getter" {
						getter.block()
					}
				}
				var elapsed atomic.Int64
				hooks.onFatal = func(err error) {
					if err == cause {
						elapsed.CompareAndSwap(0, int64(f.worker.activityWorker.worker.retrier.GetElapsedTime()))
					}
				}
				hooks.armed.Store(true)
				startResult := startupStartAsync(f.worker)
				<-getter.entered
				<-producer.started
				producer.trigger.open()
				<-f.shutdown.entered
				assert.Same(t, cause, startupFatalCause(f.worker))
				assert.Greater(t, time.Duration(elapsed.Load()), getRetryLongPollGracePeriod())
				retired := make(chan struct{})
				go func() {
					f.worker.activityWorker.worker.pollerWG.Wait()
					close(retired)
				}()
				<-retired
				// All polling goroutines finish before the held shutdown
				// RPC returns. Stop must still finish every plugin.
				stopped := lifecycleStopAsync(f.worker)
				synctest.Wait()
				startupRequireNoResult(t, startResult)
				requireLifecyclePending(t, stopped, "Stop skipped the real shutdown RPC")
				f.shutdown.open()
				<-afterStopNext.entered
				synctest.Wait()
				requireLifecyclePending(t, f.worker.stopDone, "cleanup skipped after-next StopWorker")
				startupRequireNoResult(t, startResult)
				afterStopNext.open()
				<-stopped
				assert.Nil(t, f.worker.nexusWorker)
				startupAssertNoMembership(t, f.worker)
				startupAssertCanceled(t, f.worker)
				startupRequireNoResult(t, startResult)
				// Both Stop and Start finish before this Run call, so it
				// replays the saved result without blocking the fake clock.
				getter.open()
				assert.ErrorIs(t, <-startResult, ErrWorkerShutdown)
				err := f.worker.Run(nil)
				assert.ErrorIs(t, err, ErrWorkerShutdown)
				assert.ErrorIs(t, err, cause)
				var typed *serviceerror.NamespaceNotFound
				assert.ErrorAs(t, err, &typed)
				assert.Same(t, cause, typed)
				assert.Same(t, cause, startupFatalCause(f.worker))
				assert.EqualValues(t, 1, f.shutdownCalls.Load())
				startupAssertCanceled(t, f.worker)
			})
		})
	}
}

// Run attaches to the entire saved Start attempt, including plugin failure.
// Every normal result joins cleanup and keeps startup and fatal causes intact.
func TestWorkerStartupAdmissionWholeAttemptResults(t *testing.T) {
	for _, origin := range []string{"worker", "client"} {
		for _, mode := range []string{"before_error", "before_stop_error", "before_stop_next", "after_error", "after_stop_error"} {
			t.Run(origin+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					cause := errors.New("original startup failure")
					plugin := &startupPlugin{}
					opts := startupOptions(true, false)
					clientOpts := ClientOptions{}
					if origin == "client" {
						clientOpts.Plugins = []ClientPlugin{plugin}
					} else {
						opts.Plugins = []WorkerPlugin{plugin}
					}
					f := newStartupFixture(t, opts, clientOpts, nil)
					afterStopNext := newStartupGate(t)
					plugin.stop = func(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
						next(ctx, in)
						afterStopNext.block()
					}
					f.shutdown = newStartupGate(t)
					plugin.start = func(ctx context.Context, in WorkerPluginStartWorkerOptions, next func(context.Context, WorkerPluginStartWorkerOptions) error) error {
						if strings.HasPrefix(mode, "after") {
							if err := next(ctx, in); err != nil {
								return err
							}
						}
						if strings.Contains(mode, "stop") {
							f.worker.Stop()
						}
						if mode == "before_stop_next" {
							return next(ctx, in)
						}
						return cause
					}
					before := startupCacheReferences(f.worker)
					result := lifecycleRunAsync(f.worker, nil)
					<-f.shutdown.entered
					synctest.Wait()
					startupRequireNoResult(t, result)
					f.shutdown.open()
					<-afterStopNext.entered
					synctest.Wait()
					startupRequireNoResult(t, result)
					afterStopNext.open()
					err := <-result
					if mode == "before_stop_next" {
						assert.ErrorIs(t, err, ErrWorkerShutdown)
					} else {
						assert.Same(t, cause, err)
					}
					assert.Same(t, err, f.worker.Run(nil))
					f.worker.Stop()
					assert.EqualValues(t, 1, plugin.calls.Load())
					assert.EqualValues(t, before-1, startupCacheReferences(f.worker))
					startupAssertCanceled(t, f.worker)
				})
			})
		}
	}
	t.Run("concurrent_before_core_and_attachment_after_stop", func(t *testing.T) {
		plugin := &startupPlugin{}
		f := newStartupFixture(t, startupOptionsWithPlugin(plugin), ClientOptions{}, nil)
		beforeCore := newStartupGate(t)
		cause := errors.New("held before-core error")
		plugin.start = func(context.Context, WorkerPluginStartWorkerOptions, func(context.Context, WorkerPluginStartWorkerOptions) error) error {
			beforeCore.block()
			return cause
		}
		startResult := startupStartAsync(f.worker)
		startupAwaitSignal(t, beforeCore.entered)
		runActor, runResult := startupRunAsync(t, f.worker)
		startupAwaitRunAttemptWait(t, runActor)
		startupAwaitSignal(t, lifecycleStopAsync(f.worker))
		startupRequireNoResult(t, startResult)
		startupRequireNoResult(t, runResult)
		beforeCore.open()
		assert.Same(t, cause, startupAwaitResult(t, startResult))
		assert.Same(t, cause, startupAwaitResult(t, runResult))
		assert.Same(t, cause, f.worker.Run(nil))
		assert.EqualValues(t, 1, plugin.calls.Load())
		assert.False(t, f.worker.started.Load())
	})
	for _, attempted := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopped_attempted_%t", attempted), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				plugin := &startupPlugin{}
				f := newStartupFixture(t, startupOptionsWithPlugin(plugin), ClientOptions{}, nil)
				if attempted {
					require.NoError(t, f.worker.Start())
				}
				f.worker.Stop()
				err := f.worker.Run(nil)
				if attempted {
					assert.NoError(t, err)
					assert.EqualValues(t, 1, plugin.calls.Load())
				} else {
					assert.ErrorIs(t, err, ErrWorkerShutdown)
					assert.Zero(t, plugin.calls.Load())
					assert.False(t, f.worker.started.Load())
				}
			})
		})
	}
	// The preceding controls finish their shared-cache count checks before
	// parallel workers exist. Only the two real-grace cases run together here.
	t.Parallel()
	var cases sync.WaitGroup
	for _, identical := range []bool{false, true} {
		cases.Go(func() {
			t.Run(fmt.Sprintf("actual_fatal_and_plugin_error/identical_%t", identical), func(t *testing.T) {
				fatal := serviceerror.NewNamespaceNotFound("plugin-fatal-namespace")
				var startErr error = errors.New("outer startup failure")
				if identical {
					startErr = fatal
				}
				plugin := &startupPlugin{}
				f := newStartupFixture(t, startupOptionsWithPlugin(plugin), ClientOptions{}, nil)
				f.shutdown = newStartupGate(t)
				afterStartNext := newStartupGate(t)
				afterStopNext := newStartupGate(t)
				plugin.start = func(ctx context.Context, in WorkerPluginStartWorkerOptions, next func(context.Context, WorkerPluginStartWorkerOptions) error) error {
					if err := next(ctx, in); err != nil {
						return err
					}
					afterStartNext.block()
					return startErr
				}
				plugin.stop = func(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
					next(ctx, in)
					afterStopNext.block()
				}
				producer := &startupFatalPoller{
					worker: f.worker, cause: fatal, started: make(chan struct{}), trigger: newStartupGate(t),
				}
				f.worker.activityWorker.poller = producer
				var elapsed atomic.Int64
				f.hooks.onFatal = func(err error) {
					if err == fatal {
						elapsed.CompareAndSwap(0, int64(f.worker.activityWorker.worker.retrier.GetElapsedTime()))
					}
				}
				startResult := startupStartAsync(f.worker)
				startupAwaitSignal(t, afterStartNext.entered)
				startupAwaitSignal(t, producer.started)
				runActor, runResult := startupRunAsync(t, f.worker)
				startupAwaitRunAttemptWait(t, runActor)
				producer.trigger.open()
				startupAwaitSignal(t, f.shutdown.entered)
				assert.Same(t, fatal, startupFatalCause(f.worker))
				assert.Greater(t, time.Duration(elapsed.Load()), getRetryLongPollGracePeriod())
				afterStartNext.open()
				assert.Same(t, startErr, startupAwaitResult(t, startResult))
				startupAwaitRunCleanupWait(t, runActor)
				startupRequireNoResult(t, runResult)
				f.shutdown.open()
				startupAwaitSignal(t, afterStopNext.entered)
				startupAwaitRunCleanupWait(t, runActor)
				startupRequireNoResult(t, runResult)
				afterStopNext.open()
				err := startupAwaitResult(t, runResult)
				assert.ErrorIs(t, err, startErr)
				assert.ErrorIs(t, err, fatal)
				joined, ok := err.(interface{ Unwrap() []error })
				require.True(t, ok)
				assert.Equal(t, []error{startErr, fatal}, joined.Unwrap())
				assert.EqualValues(t, 1, plugin.calls.Load())
				startupAssertCanceled(t, f.worker)
			})
		})
	}
	cases.Wait()
}

// Startup panics are synchronous and replay from the saved attempt. Explicit
// Stop still cleans owned resources; a cleanup panic does not report completion.
func TestWorkerStartupAdmissionSynchronousPanic(t *testing.T) {
	for _, origin := range []string{"getter", "worker_plugin", "client_plugin"} {
		t.Run(origin, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				panicValue := &struct{ message string }{"synchronous startup panic"}
				plugin := &startupPlugin{}
				opts := startupOptions(true, false)
				clientOpts := ClientOptions{}
				if origin == "worker_plugin" {
					opts.Plugins = []WorkerPlugin{plugin}
				} else if origin == "client_plugin" {
					clientOpts.Plugins = []ClientPlugin{plugin}
				}
				hooks := &startupHooks{}
				f := newStartupFixture(t, opts, clientOpts, hooks)
				if origin == "getter" {
					f.worker.RegisterNexusService(startupService(t))
					hooks.onEvent = func(event string) {
						if event == "nexus.getter" {
							panic(panicValue)
						}
					}
					hooks.armed.Store(true)
				} else {
					plugin.start = func(context.Context, WorkerPluginStartWorkerOptions, func(context.Context, WorkerPluginStartWorkerOptions) error) error {
						panic(panicValue)
					}
				}
				assert.PanicsWithValue(t, panicValue, func() {
					err := f.worker.Start()
					t.Errorf("first Start returned %v", err)
				})
				assert.PanicsWithValue(t, panicValue, func() {
					err := f.worker.Start()
					t.Errorf("repeated Start returned %v", err)
				})
				assert.PanicsWithValue(t, panicValue, func() {
					err := f.worker.Run(nil)
					t.Errorf("Run returned %v", err)
				})
				requireLifecyclePending(t, f.worker.stopDone, "synchronous startup panic was converted into normal cleanup")
				f.worker.Stop()
				startupAssertCanceled(t, f.worker)
				if origin == "getter" {
					assert.EqualValues(t, 1, f.tuner.getters.Load())
					assert.Nil(t, f.worker.nexusWorker)
				} else {
					assert.EqualValues(t, 1, plugin.calls.Load())
				}
			})
		})
	}
	t.Run("validation_before_allocation_and_locked_unwind", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := newStartupFixture(t, startupOptions(false, false), ClientOptions{}, nil)
			// Malformed direct SDK pollers validate before resource allocation.
			// This does not exercise an unreachable post-allocation Nexus panic.
			prepared := prepareBaseWorker(baseWorkerOptions{
				logger: f.worker.logger, metricsHandler: metrics.NopHandler,
				slotSupplier: f.tuner.nexus, taskPollers: []scalableTaskPoller{{}, {}},
			})
			assert.Panics(t, prepared.initializeResources)
			assert.Nil(t, prepared.limiterContext)
			assert.Nil(t, prepared.taskLimiterContext)
			// A real aggregate initialization panic releases its deferred lock.
			f.worker.workflowWorker.worker.options.taskPollers = []scalableTaskPoller{}
			assert.PanicsWithValue(t, "task pollers already initialized", func() {
				err := f.worker.Start()
				t.Errorf("invalid initialization returned %v", err)
			})
			require.True(t, f.worker.lifecycleMu.TryLock(), "validation panic retained the startup mutex")
			f.worker.lifecycleMu.Unlock()
			f.worker.Stop()
			startupAssertCanceled(t, f.worker)
		})
	})
	t.Run("cleanup_panic_does_not_report_completion", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			panicValue := "after child cleanup"
			plugin := &startupPlugin{
				stop: func(ctx context.Context, in WorkerPluginStopWorkerOptions, next func(context.Context, WorkerPluginStopWorkerOptions)) {
					next(ctx, in)
					panic(panicValue)
				},
			}
			f := newStartupFixture(t, startupOptionsWithPlugin(plugin), ClientOptions{}, nil)
			f.autoStop = false
			assert.PanicsWithValue(t, panicValue, f.worker.Stop)
			requireLifecyclePending(t, f.worker.stopDone, "panicking Stop falsely closed stopDone")
			for _, ctx := range startupContexts(f.worker) {
				requireLifecycleClosed(t, ctx.Done(), "supplied next skipped child cleanup")
			}
			// The deliberately failed Stop cannot be joined again. Children
			// already stopped through next; release the remaining cache lease.
			f.worker.cacheLease.release()
		})
	})
}
