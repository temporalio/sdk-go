package serverlesslambdaworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// testInvocationContext returns a context with an immediate deadline, simulating
// a Lambda invocation that has already reached its shutdown window.
func testInvocationContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now())
	_ = cancel
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
		startLambda: func(handler interface{}, options ...lambda.Option) {
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
	}
	return deps, w, c
}

func TestRunWorkerInternal_Success(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("RegisterWorkflow", mock.Anything).Once()
	w.On("RegisterActivity", mock.Anything).Once()
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return errors.New("bad config")
	}, deps)
	assert.ErrorContains(t, err, "configure callback failed")
}

func TestRunWorkerInternal_LoadConfigError(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.loadConfig = func() (client.Options, error) {
		return client.Options{}, errors.New("config load error")
	}
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)
	assert.ErrorContains(t, err, "loading client config")
}

func TestRunWorkerInternal_DialError(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.dial = func(client.Options) (client.Client, error) {
		return nil, errors.New("connection refused")
	}
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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
	deps.startLambda = func(handler interface{}, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			handlerErr = h(testInvocationContext())
		}
	}

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	assert.NoError(t, err) // runWorkerInternal itself succeeds.
	assert.ErrorContains(t, handlerErr, "dialing Temporal server")
}

func TestRunWorkerInternal_MissingTaskQueue(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.getenv = func(string) string { return "" }

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)
	assert.ErrorContains(t, err, "task queue not configured")
}

func TestRunWorkerInternal_WorkerStartError(t *testing.T) {
	deps, w, c := newTestDeps()
	w.On("Start").Return(errors.New("start failed")).Once()
	c.On("Close").Once()

	var handlerErr error
	deps.startLambda = func(handler interface{}, options ...lambda.Option) {
		if h, ok := handler.(func(context.Context) error); ok {
			handlerErr = h(testInvocationContext())
		}
	}

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		ctx.SetTaskQueue("user-queue")
		ctx.MutateClientOptions(func(opts *client.Options) error {
			opts.Namespace = "custom-ns"
			return nil
		})
		ctx.MutateWorkerOptions(func(opts *worker.Options) error {
			opts.MaxConcurrentActivityExecutionSize = 99
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "custom-ns", capturedClientOpts.Namespace)
	assert.Equal(t, 99, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, defaultMaxConcurrentActivityExecutionSize, capturedWorkerOpts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, defaultMaxConcurrentWorkflowTaskExecutionSize, capturedWorkerOpts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, defaultWorkerStopTimeout, capturedWorkerOpts.WorkerStopTimeout)
	assert.True(t, capturedWorkerOpts.DisableEagerActivities)
}

func TestRunWorkerInternal_SetsCacheSize(t *testing.T) {
	deps, w, c := newTestDeps()
	var cacheSize int
	deps.setCacheSize = func(size int) { cacheSize = size }

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, defaultStickyCacheSize, cacheSize)
}

func TestRunWorkerInternal_CleanupPerInvocation(t *testing.T) {
	deps, w, c := newTestDeps()

	// Verify the worker is stopped and client is closed within each invocation.
	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		ctx.SetTaskQueue("explicit-queue")
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "explicit-queue", capturedTaskQueue)
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "req-abc-123@arn:aws:lambda:us-east-1:123456:function:my-func", capturedClientOpts.Identity)
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		ctx.MutateClientOptions(func(opts *client.Options) error {
			opts.Identity = "my-custom-identity"
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, "my-custom-identity", capturedClientOpts.Identity)
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	require.NoError(t, err)
	// Identity left empty for the SDK to fill with its default.
	assert.Empty(t, capturedClientOpts.Identity)
}

func TestRunWorkerInternal_OnShutdownCalled(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	shutdownCalled := false
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		ctx.OnShutdown(func(context.Context) error {
			shutdownCalled = true
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.True(t, shutdownCalled, "shutdown function should be called during invocation")
}

func TestRunWorkerInternal_OnShutdownCalledPerInvocation(t *testing.T) {
	deps, w, c := newTestDeps()
	deps.startLambda = func(handler interface{}, options ...lambda.Option) {
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
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		ctx.OnShutdown(func(context.Context) error {
			shutdownCount++
			return nil
		})
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 3, shutdownCount, "shutdown function should be called once per invocation")
}

func TestRunWorkerInternal_OnShutdownMultipleFuncs(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	var order []string
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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
}

func TestRunWorkerInternal_OnShutdownErrorLogged(t *testing.T) {
	deps, w, c := newTestDeps()

	w.On("Start").Return(nil).Once()
	w.On("Stop").Once()
	c.On("Close").Once()

	secondCalled := false
	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
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
}

func TestRunWorkerInternal_PerInvocationLifecycle(t *testing.T) {
	dialCount := 0
	deps, w, c := newTestDeps()
	deps.dial = func(opts client.Options) (client.Client, error) {
		dialCount++
		return c, nil
	}
	deps.startLambda = func(handler interface{}, options ...lambda.Option) {
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

	err := runWorkerInternal(func(ctx *ConfigureWorkerContext) error {
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 3, dialCount, "client.Dial should be called once per invocation")
	w.AssertExpectations(t)
	c.AssertExpectations(t)
}
