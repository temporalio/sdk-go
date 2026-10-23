package lambdaworker

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// testInvocationContext returns a context with a far-future deadline that is
// already cancelled. The deadline is far enough out to avoid triggering the
// work-time checks, while the cancellation causes the handler to proceed to
// shutdown immediately.
func testInvocationContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	cancel()
	return ctx
}

// mockClient implements client.Client for testing.
type mockClient struct {
	mock.Mock
	client.Client
}

func (m *mockClient) Close() {
	m.Called()
}

func newTestDeps() (workerDeps, *mockWorker, *mockClient) {
	w := &mockWorker{}
	c := &mockClient{}

	deps := workerDeps{
		dial: func(opts client.Options) (client.Client, error) {
			return c, nil
		},
		newWorker: func(_ client.Client, _ string, _ worker.Options) worker.Worker {
			return w
		},
		startLambda: func(handler any, options ...lambda.Option) {
			// Invoke the handler once to simulate a single Lambda invocation.
			if h, ok := handler.(func(context.Context) error); ok {
				_ = h(testInvocationContext())
			}
		},
		loadConfig: func() (client.Options, error) {
			return client.Options{}, nil
		},
		getenv: func(k string) string {
			if k == envTaskQueue {
				return "test-queue"
			}
			return ""
		},
		setCacheSize: func(int) {},
		exit:         func(int) {},
		extractLambdaCtx: func(context.Context) (string, string, bool) {
			return "req-123", "arn:aws:lambda:us-east-1:123:function:my-func", true
		},
		jitter: func(time.Duration) time.Duration { return 0 },
	}
	return deps, w, c
}

var testVersion = worker.WorkerDeploymentVersion{
	DeploymentName: "test-deployment",
	BuildID:        "test-build",
}

func TestRunWorkerInternal_Success(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("RegisterWorkflow", mock.Anything).Once()
	w.On("RegisterActivity", mock.Anything).Once()
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.RegisterWorkflow(myWorkflow)
		ctx.RegisterActivity(myActivity)
		return nil
	}, deps)

	require.NoError(t, err)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_ConfigureCallbackError(t *testing.T) {
	deps, _, _ := newTestDeps()
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return errors.New("bad config")
	}, deps)
	assert.ErrorContains(t, err, "configure callback failed")
}

func TestRunWorkerInternal_LoadConfigError(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.loadConfig = func() (client.Options, error) {
		return client.Options{}, errors.New("config load error")
	}
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)
	assert.ErrorContains(t, err, "loading client config")
}

func TestRunWorkerInternal_DialError(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.dial = func(client.Options) (client.Client, error) {
		return nil, errors.New("connection refused")
	}
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)
	// Dial error surfaces via the handler return, but startLambda swallows it.
	// Verify no panic occurs.
	assert.NoError(t, err)
}

func TestRunWorkerInternal_DialError_HandlerReturnsError(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.dial = func(client.Options) (client.Client, error) {
		return nil, errors.New("connection refused")
	}

	var handlerErr error
	deps.startLambda = func(handler any, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			handlerErr = h(testInvocationContext())
		}
	}

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	assert.NoError(t, err) // runWorkerInternal itself succeeds.
	assert.ErrorContains(t, handlerErr, "dialing Temporal server")
}

func TestRunWorkerInternal_MissingTaskQueue(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.getenv = func(string) string { return "" }

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)
	assert.ErrorContains(t, err, "task queue not configured")
}

func TestRunWorkerInternal_MissingVersion(t *testing.T) {
	deps, _, _ := newTestDeps()
	err := runWorkerInternal(worker.WorkerDeploymentVersion{}, func(ctx *Options) error {
		return nil
	}, deps)
	assert.ErrorContains(t, err, "version is required")
}

func TestRunWorkerInternal_WorkerStartError(t *testing.T) {
	deps, w, c := newTestDeps()
	w.On("Start").Return(errors.New("start failed")).Once()
	c.On("Close").Once()

	var handlerErr error
	deps.startLambda = func(handler any, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			handlerErr = h(testInvocationContext())
		}
	}

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)
	assert.NoError(t, err) // runWorkerInternal itself succeeds.
	assert.ErrorContains(t, handlerErr, "starting worker")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_UserOverridesApplied(t *testing.T) {
	var capturedClientOpts client.Options
	var capturedWorkerOpts worker.Options

	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		capturedClientOpts = opts
		return c, nil
	}
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.TaskQueue = "user-queue"
		ctx.ClientOptions.Namespace = "custom-ns"
		ctx.WorkerOptions.MaxConcurrentActivityExecutionSize = 99
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "custom-ns", capturedClientOpts.Namespace)
	assert.Equal(t, 99, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_LambdaDefaultsApplied(t *testing.T) {
	var capturedWorkerOpts worker.Options

	deps, w, c := newTestDeps()
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, defaultMaxConcurrentActivityExecutionSize, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, defaultMaxConcurrentWorkflowTaskExecutionSize, capturedWorkerOpts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, defaultWorkerStopTimeout, capturedWorkerOpts.WorkerStopTimeout)
	assert.True(t, capturedWorkerOpts.DisableEagerActivities)
	assert.True(t, capturedWorkerOpts.DeploymentOptions.UseVersioning)
	assert.Equal(t, testVersion, capturedWorkerOpts.DeploymentOptions.Version)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_SetsCacheSize(t *testing.T) {
	deps, w, c := newTestDeps()
	var cacheSize int
	deps.setCacheSize = func(size int) { cacheSize = size }

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, defaultStickyCacheSize, cacheSize)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_CleanupPerInvocation(t *testing.T) {
	deps, w, c := newTestDeps()

	// Verify the worker is stopped and client is closed within each invocation.
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_TaskQueueFromSetTaskQueue(t *testing.T) {
	var capturedTaskQueue string
	deps, w, c := newTestDeps()
	deps.getenv = func(string) string { return "" } // No env vars.
	deps.newWorker = func(_ client.Client, tq string, _ worker.Options) worker.Worker {
		capturedTaskQueue = tq
		return w
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.TaskQueue = "explicit-queue"
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "explicit-queue", capturedTaskQueue)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdentityFromLambdaContext(t *testing.T) {
	var capturedClientOpts client.Options
	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		capturedClientOpts = opts
		return c, nil
	}
	deps.extractLambdaCtx = func(context.Context) (string, string, bool) {
		return "req-abc-123", "arn:aws:lambda:us-east-1:123456:function:my-func", true
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "req-abc-123@arn:aws:lambda:us-east-1:123456:function:my-func", capturedClientOpts.Identity)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdentityUserOverrideWins(t *testing.T) {
	var capturedClientOpts client.Options
	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		capturedClientOpts = opts
		return c, nil
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.ClientOptions.Identity = "my-custom-identity"
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "my-custom-identity", capturedClientOpts.Identity)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdentityNoLambdaContext(t *testing.T) {
	var capturedClientOpts client.Options
	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		capturedClientOpts = opts
		return c, nil
	}
	deps.extractLambdaCtx = func(context.Context) (string, string, bool) {
		return "", "", false
	}

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	// Identity left empty for the SDK to fill with its default.
	assert.Empty(t, capturedClientOpts.Identity)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownCalled(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	shutdownCalled := false
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(context.Context) error {
			shutdownCalled = true
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.True(t, shutdownCalled, "shutdown function should be called during invocation")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownCalledPerInvocation(t *testing.T) {
	deps, w, c := newTestDeps()
	deps.startLambda = func(handler any, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			_ = h(testInvocationContext())
			_ = h(testInvocationContext())
			_ = h(testInvocationContext())
		}
	}

	w.On("Start").Return(nil).Times(3)
	w.On("Stop").Times(3)
	c.On("Close").Times(3)

	shutdownCount := 0
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(context.Context) error {
			shutdownCount++
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 3, shutdownCount, "shutdown function should be called once per invocation")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownMultipleFuncs(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	var order []string
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(context.Context) error {
			order = append(order, "first")
			return nil
		})
		ctx.OnShutdown(func(context.Context) error {
			order = append(order, "second")
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second"}, order, "shutdown functions should run in registration order")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownErrorLogged(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	secondCalled := false
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(context.Context) error {
			return errors.New("flush failed")
		})
		ctx.OnShutdown(func(context.Context) error {
			secondCalled = true
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.True(t, secondCalled, "subsequent shutdown functions should still run after an error")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownReceivesFreshContext(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(hookCtx context.Context) error {
			// The hook context must not be cancelled — this is critical
			// for OTLP flushes that check ctx.Err() before sending.
			assert.NoError(t, hookCtx.Err(),
				"shutdown hook should receive a non-cancelled context")
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_OnShutdownRunsAfterWorkerStop(t *testing.T) {
	deps, w, c := newTestDeps()

	var order []string
	w.On("Start").Return(nil).Once()
	w.On("Stop").Run(func(mock.Arguments) {
		order = append(order, "worker.Stop")
	}).Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.OnShutdown(func(context.Context) error {
			order = append(order, "shutdown-hook")
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, []string{"worker.Stop", "shutdown-hook"}, order,
		"shutdown hooks must run after worker.Stop()")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_TightDeadlineReturnsError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()

		// 2s deadline with 1500ms buffer → ~500ms workTime (≤ 1s), error.
		deps.startLambda = func(handler any, options ...lambda.Option) {
			if h, ok := handler.(func(context.Context) error); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				handlerErr := h(ctx)
				assert.ErrorContains(t, handlerErr,
					"almost no time for work")
			}
		}

		err := runWorkerInternal(testVersion, func(ctx *Options) error {
			ctx.ShutdownDeadlineBuffer = 1500 * time.Millisecond
			return nil
		}, deps)
		require.NoError(t, err)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

type capturingLogger struct {
	infos  []string
	warns  []string
	errors []string
}

func (l *capturingLogger) Debug(string, ...any) {}
func (l *capturingLogger) Info(msg string, _ ...any) {
	l.infos = append(l.infos, msg)
}
func (l *capturingLogger) Warn(msg string, _ ...any) {
	l.warns = append(l.warns, msg)
}
func (l *capturingLogger) Error(msg string, _ ...any) {
	l.errors = append(l.errors, msg)
}

func TestRunWorkerInternal_TightDeadlineLogsWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		logger := &capturingLogger{}
		deps.loadConfig = func() (client.Options, error) {
			return client.Options{Logger: logger}, nil
		}

		// Use a small custom buffer so workTime lands in the warning band
		// (> 1s but < 5s) without a long wait. 2s deadline, 500ms buffer → ~1.5s
		// work time.
		deps.startLambda = func(handler any, options ...lambda.Option) {
			if h, ok := handler.(func(context.Context) error); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = h(ctx)
			}
		}

		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		err := runWorkerInternal(testVersion, func(ctx *Options) error {
			ctx.ShutdownDeadlineBuffer = 500 * time.Millisecond
			return nil
		}, deps)

		require.NoError(t, err)
		require.Len(t, logger.warns, 1)
		assert.Contains(t, logger.warns[0], "less than 5s for work")
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_PerInvocationLifecycle(t *testing.T) {
	dialCount := 0
	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		dialCount++
		return c, nil
	}
	deps.startLambda = func(handler any, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			// Invoke handler multiple times to simulate sequential Lambda invocations.
			_ = h(testInvocationContext())
			_ = h(testInvocationContext())
			_ = h(testInvocationContext())
		}
	}

	// Each invocation creates and tears down its own worker and client.
	w.On("Start").Return(nil).Times(3)
	w.On("Stop").Times(3)
	c.On("Close").Times(3)

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 3, dialCount, "client.Dial should be called once per invocation")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

// runIdleTestInvocation runs a single invocation with an hour-long deadline and returns how long
// the handler took and what it returned.
func runIdleTestInvocation(
	t *testing.T,
	deps workerDeps,
	configure func(ctx *Options) error,
) (elapsed time.Duration, handlerErr error) {
	t.Helper()
	deps.startLambda = func(handler any, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()
			start := time.Now()
			handlerErr = h(ctx)
			elapsed = time.Since(start)
		}
	}
	require.NoError(t, runWorkerInternal(testVersion, configure, deps))
	return elapsed, handlerErr
}

func TestRunWorkerInternal_IdleTimeoutShutsDownEarly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		logger := &capturingLogger{}
		deps.loadConfig = func() (client.Options, error) {
			return client.Options{Logger: logger}, nil
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		elapsed, handlerErr := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 10 * time.Second
			return nil
		})

		require.NoError(t, handlerErr)
		assert.GreaterOrEqual(t, elapsed, 10*time.Second+idleCheckInterval)
		assert.Less(t, elapsed, 10*time.Second+2*idleCheckInterval)
		assert.Contains(t, logger.infos, "Shutting down idle worker before the invocation deadline")
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleTimeoutWaitsForTaskInHand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		var capturedWorkerOpts worker.Options
		deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
			capturedWorkerOpts = opts
			return w
		}
		// Simulate a workflow task that is held for 30s after the worker starts.
		w.On("Start").Run(func(mock.Arguments) {
			s := capturedWorkerOpts.Tuner.GetWorkflowTaskSlotSupplier()
			permit := reserveSlot(t, s)
			markSlotUsed(s, permit)
			go func() {
				time.Sleep(30 * time.Second)
				releaseSlot(s, permit)
			}()
		}).Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		elapsed, handlerErr := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 10 * time.Second
			return nil
		})

		require.NoError(t, handlerErr)
		assert.GreaterOrEqual(t, elapsed, 40*time.Second, "worker must be idle for the full timeout")
		assert.Less(t, elapsed, 40*time.Second+2*idleCheckInterval)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleTimeoutInstallsCountingTuner(t *testing.T) {
	var capturedWorkerOpts worker.Options
	deps, w, c := newTestDeps()
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.IdleTimeout = 10 * time.Second
		ctx.WorkerOptions.MaxConcurrentActivityExecutionSize = 7
		return nil
	}, deps)

	require.NoError(t, err)
	require.NotNil(t, capturedWorkerOpts.Tuner)
	assert.IsType(t, &countingSlotSupplier{}, capturedWorkerOpts.Tuner.GetActivityTaskSlotSupplier())
	assert.Equal(t, 7, capturedWorkerOpts.Tuner.GetActivityTaskSlotSupplier().MaxSlots())
	assert.Equal(t, defaultMaxConcurrentWorkflowTaskExecutionSize,
		capturedWorkerOpts.Tuner.GetWorkflowTaskSlotSupplier().MaxSlots())
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentLocalActivityExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentNexusTaskExecutionSize)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdleTimeoutWrapsUserTuner(t *testing.T) {
	var capturedWorkerOpts worker.Options
	deps, w, c := newTestDeps()
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	userTuner, err := worker.NewFixedSizeTuner(worker.FixedSizeTunerOptions{NumActivitySlots: 3})
	require.NoError(t, err)
	err = runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.IdleTimeout = 10 * time.Second
		ctx.WorkerOptions.Tuner = userTuner
		return nil
	}, deps)

	require.NoError(t, err)
	require.NotNil(t, capturedWorkerOpts.Tuner)
	assert.NotSame(t, userTuner, capturedWorkerOpts.Tuner)
	activitySupplier, ok := capturedWorkerOpts.Tuner.GetActivityTaskSlotSupplier().(*countingSlotSupplier)
	require.True(t, ok)
	assert.Same(t, userTuner.GetActivityTaskSlotSupplier(), activitySupplier.inner)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_UserTunerClearsConcurrencyDefaults(t *testing.T) {
	var capturedWorkerOpts worker.Options
	deps, w, c := newTestDeps()
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	userTuner, err := worker.NewFixedSizeTuner(worker.FixedSizeTunerOptions{})
	require.NoError(t, err)
	err = runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.WorkerOptions.Tuner = userTuner
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Same(t, userTuner, capturedWorkerOpts.Tuner)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentLocalActivityExecutionSize)
	assert.Zero(t, capturedWorkerOpts.MaxConcurrentNexusTaskExecutionSize)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_ResourceBasedTunerDisablesIdleShutdown(t *testing.T) {
	var capturedWorkerOpts worker.Options
	deps, w, c := newTestDeps()
	logger := &capturingLogger{}
	deps.loadConfig = func() (client.Options, error) {
		return client.Options{Logger: logger}, nil
	}
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	fixed, err := worker.NewFixedSizeSlotSupplier(1)
	require.NoError(t, err)
	userTuner, err := worker.NewCompositeTuner(worker.CompositeTunerOptions{
		WorkflowSlotSupplier:        fixed,
		ActivitySlotSupplier:        &worker.ResourceBasedSlotSupplier{},
		LocalActivitySlotSupplier:   fixed,
		NexusSlotSupplier:           fixed,
		SessionActivitySlotSupplier: fixed,
	})
	require.NoError(t, err)
	err = runWorkerInternal(testVersion, func(ctx *Options) error {
		ctx.IdleTimeout = 10 * time.Second
		ctx.WorkerOptions.Tuner = userTuner
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Same(t, userTuner, capturedWorkerOpts.Tuner)
	require.Len(t, logger.warns, 1)
	assert.Contains(t, logger.warns[0], "Idle shutdown is disabled")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdleTimeoutFromEnv(t *testing.T) {
	deps, w, c := newTestDeps()
	deps.getenv = func(k string) string {
		switch k {
		case envTaskQueue:
			return "test-queue"
		case envIdleTimeout:
			return "30"
		}
		return ""
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	var idleTimeout time.Duration
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		idleTimeout = ctx.IdleTimeout
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, idleTimeout)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_IdleTimeoutDisabledByDefault(t *testing.T) {
	var capturedWorkerOpts worker.Options
	deps, w, c := newTestDeps()
	deps.newWorker = func(_ client.Client, _ string, opts worker.Options) worker.Worker {
		capturedWorkerOpts = opts
		return w
	}
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		assert.Zero(t, ctx.IdleTimeout)
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Nil(t, capturedWorkerOpts.Tuner)
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}

func TestRunWorkerInternal_InvalidIdleTimeout(t *testing.T) {
	for _, v := range []string{"soon", "30s", "7.5"} {
		t.Run("env var "+v, func(t *testing.T) {
			deps, _, _ := newTestDeps()
			deps.getenv = func(k string) string {
				if k == envIdleTimeout {
					return v
				}
				return "test-queue"
			}
			err := runWorkerInternal(testVersion, func(ctx *Options) error { return nil }, deps)
			assert.ErrorContains(t, err, envIdleTimeout)
		})
	}

	for _, timeout := range []time.Duration{-time.Second, time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			deps, _, _ := newTestDeps()
			err := runWorkerInternal(testVersion, func(ctx *Options) error {
				ctx.IdleTimeout = timeout
				return nil
			}, deps)
			assert.ErrorContains(t, err, "invalid idle timeout")
		})
	}
}

func TestRunWorkerInternal_IdleTimeoutJitter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		var maxJitter time.Duration
		deps.jitter = func(n time.Duration) time.Duration {
			maxJitter = n
			return n - 1
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		elapsed, handlerErr := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 10 * time.Second
			return nil
		})

		require.NoError(t, handlerErr)
		assert.Equal(t, 2*time.Second, maxJitter)
		assert.GreaterOrEqual(t, elapsed, 10*time.Second+idleCheckInterval+maxJitter-1)
		assert.Less(t, elapsed, 10*time.Second+2*idleCheckInterval+maxJitter)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}
