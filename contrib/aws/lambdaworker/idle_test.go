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

const testIdleThreshold = 5 * time.Second

// timeWaitingForIdle returns how long waitForIdle takes to return with testIdleThreshold.
func timeWaitingForIdle(t *testing.T, getUsage func(start time.Time, elapsed time.Duration) worker.SlotUsage) time.Duration {
	t.Helper()
	start := time.Now()
	err := waitForIdle(t.Context(), func() (worker.SlotUsage, error) {
		return getUsage(start, time.Since(start)), nil
	}, testIdleThreshold)
	require.NoError(t, err)
	return time.Since(start)
}

func TestWaitForIdle_IdleFromStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		elapsed := timeWaitingForIdle(t, func(time.Time, time.Duration) worker.SlotUsage {
			return worker.SlotUsage{}
		})
		assert.Equal(t, testIdleThreshold, elapsed)
	})
}

func TestWaitForIdle_ChangeBeforeStartIgnored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		elapsed := timeWaitingForIdle(t, func(start time.Time, _ time.Duration) worker.SlotUsage {
			return worker.SlotUsage{Workflow: worker.SlotKindUsage{UsedSlotsLastChanged: start.Add(-time.Hour)}}
		})
		assert.Equal(t, testIdleThreshold, elapsed)
	})
}

func TestWaitForIdle_ClockStartsWhenTaskFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := 3500 * time.Millisecond
		elapsed := timeWaitingForIdle(t, func(start time.Time, elapsed time.Duration) worker.SlotUsage {
			if elapsed < finish {
				return worker.SlotUsage{Activity: worker.SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: start}}
			}
			return worker.SlotUsage{Activity: worker.SlotKindUsage{UsedSlotsLastChanged: start.Add(finish)}}
		})
		// Exact, even though the finish falls between reads.
		assert.Equal(t, finish+testIdleThreshold, elapsed)
	})
}

func TestWaitForIdle_ClockRestartsForTaskBetweenReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		taskAt := 2500 * time.Millisecond
		elapsed := timeWaitingForIdle(t, func(start time.Time, elapsed time.Duration) worker.SlotUsage {
			// A workflow task starts and finishes at taskAt, while waitForIdle is waiting out the
			// threshold, so no read sees it in progress.
			if elapsed < taskAt {
				return worker.SlotUsage{}
			}
			return worker.SlotUsage{Workflow: worker.SlotKindUsage{UsedSlotsLastChanged: start.Add(taskAt)}}
		})
		assert.Equal(t, taskAt+testIdleThreshold, elapsed)
	})
}

func TestWaitForIdle_LatestChangeAcrossKinds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		elapsed := timeWaitingForIdle(t, func(start time.Time, _ time.Duration) worker.SlotUsage {
			return worker.SlotUsage{
				Workflow: worker.SlotKindUsage{UsedSlotsLastChanged: start.Add(time.Second)},
				Nexus:    worker.SlotKindUsage{UsedSlotsLastChanged: start.Add(2 * time.Second)},
			}
		})
		assert.Equal(t, 2*time.Second+testIdleThreshold, elapsed)
	})
}

func TestWaitForIdle_ContextCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		err := waitForIdle(ctx, func() (worker.SlotUsage, error) {
			return worker.SlotUsage{Nexus: worker.SlotKindUsage{UsedSlots: 1}}, nil
		}, testIdleThreshold)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestWaitForIdle_UsageError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		err := waitForIdle(t.Context(), func() (worker.SlotUsage, error) {
			return worker.SlotUsage{}, errors.New("no slot usage")
		}, testIdleThreshold)
		assert.ErrorContains(t, err, "no slot usage")
	})
}

func TestIdleThreshold(t *testing.T) {
	var maxJitter time.Duration
	threshold := idleThreshold(10*time.Second, func(n time.Duration) time.Duration {
		maxJitter = n
		return n - 1
	})
	assert.Equal(t, 2*time.Second, maxJitter)
	assert.Equal(t, 12*time.Second-1, threshold)
}

func runIdleTestInvocation(t *testing.T, deps workerDeps, configure func(*Options) error) time.Duration {
	t.Helper()
	return runIdleTestInvocationWithContext(t, deps, configure, func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 10*time.Minute)
	})
}

func runIdleTestInvocationWithContext(
	t *testing.T,
	deps workerDeps,
	configure func(*Options) error,
	invocationCtx func() (context.Context, context.CancelFunc),
) time.Duration {
	t.Helper()
	var elapsed time.Duration
	deps.startLambda = func(handler any, options ...lambda.Option) {
		h, ok := handler.(func(context.Context) error)
		require.True(t, ok)
		ctx, cancel := invocationCtx()
		defer cancel()
		start := time.Now()
		require.NoError(t, h(ctx))
		elapsed = time.Since(start)
	}
	require.NoError(t, runWorkerInternal(testVersion, configure, deps))
	return elapsed
}

func TestRunWorkerInternal_IdleShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		logger := &capturingLogger{}
		deps.loadConfig = func() (client.Options, error) {
			return client.Options{Logger: logger}, nil
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		deps.jitter = func(n time.Duration) time.Duration { return n / 2 }

		elapsed := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 30 * time.Second
			return nil
		})

		assert.Equal(t, 33*time.Second, elapsed)
		assert.Contains(t, logger.infos, "Worker idle, shutting down")
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleShutdownWithoutDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		logger := &capturingLogger{}
		deps.loadConfig = func() (client.Options, error) {
			return client.Options{Logger: logger}, nil
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		elapsed := runIdleTestInvocationWithContext(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 30 * time.Second
			return nil
		}, func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		})

		assert.Equal(t, 30*time.Second, elapsed)
		assert.Equal(t, []string{"Worker idle, shutting down"}, logger.infos)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleShutdownWaitsForTaskInProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		// Simulate a workflow task that is held for 30s after the worker starts.
		var startedAt time.Time
		w.On("Start").Run(func(mock.Arguments) { startedAt = time.Now() }).Return(nil).Once()
		deps.slotUsage = func(worker.Worker) (worker.SlotUsage, error) {
			finishedAt := startedAt.Add(30 * time.Second)
			if time.Now().Before(finishedAt) {
				return worker.SlotUsage{Workflow: worker.SlotKindUsage{UsedSlots: 1, UsedSlotsLastChanged: startedAt}}, nil
			}
			return worker.SlotUsage{Workflow: worker.SlotKindUsage{UsedSlotsLastChanged: finishedAt}}, nil
		}
		w.On("Stop").Once()
		c.On("Close").Once()

		elapsed := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 10 * time.Second
			return nil
		})

		// The worker must be idle for the full timeout after the task finishes.
		assert.Equal(t, 40*time.Second, elapsed)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_BusyWorkerRunsUntilDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		deps.slotUsage = func(worker.Worker) (worker.SlotUsage, error) {
			return worker.SlotUsage{Activity: worker.SlotKindUsage{UsedSlots: 1}}, nil
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		var buffer time.Duration
		elapsed := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 30 * time.Second
			buffer = ctx.ShutdownDeadlineBuffer
			return nil
		})

		assert.Equal(t, 10*time.Minute-buffer, elapsed)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleShutdownDisabledByDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		deps.slotUsage = func(worker.Worker) (worker.SlotUsage, error) {
			t.Error("slot usage should not be read when idle shutdown is disabled")
			return worker.SlotUsage{}, nil
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		var buffer time.Duration
		elapsed := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			assert.Zero(t, ctx.IdleTimeout)
			buffer = ctx.ShutdownDeadlineBuffer
			return nil
		})

		assert.Equal(t, 10*time.Minute-buffer, elapsed)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_SlotUsageErrorDisablesIdleShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deps, w, c := newTestDeps()
		logger := &capturingLogger{}
		deps.loadConfig = func() (client.Options, error) {
			return client.Options{Logger: logger}, nil
		}
		deps.slotUsage = func(worker.Worker) (worker.SlotUsage, error) {
			return worker.SlotUsage{}, errors.New("no slot usage")
		}
		w.On("Start").Return(nil).Once()
		w.On("Stop").Once()
		c.On("Close").Once()

		var buffer time.Duration
		elapsed := runIdleTestInvocation(t, deps, func(ctx *Options) error {
			ctx.IdleTimeout = 30 * time.Second
			buffer = ctx.ShutdownDeadlineBuffer
			return nil
		})

		assert.Equal(t, 10*time.Minute-buffer, elapsed)
		assert.Equal(t, []string{"Idle shutdown disabled for this invocation"}, logger.warns)
		w.AssertExpectations(t)
		c.AssertExpectations(t)
	})
}

func TestRunWorkerInternal_IdleTimeoutFromEnv(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.getenv = func(k string) string {
		switch k {
		case envTaskQueue:
			return "test-queue"
		case envIdleTimeout:
			return "120"
		}
		return ""
	}
	deps.startLambda = func(any, ...lambda.Option) {}

	var idleTimeout time.Duration
	err := runWorkerInternal(testVersion, func(ctx *Options) error {
		idleTimeout = ctx.IdleTimeout
		return nil
	}, deps)

	require.NoError(t, err)
	assert.Equal(t, 2*time.Minute, idleTimeout)
}

func TestRunWorkerInternal_InvalidIdleTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		option  time.Duration
		wantErr string
	}{
		{name: "unparseable env", env: "soon", wantErr: "parsing " + envIdleTimeout},
		{name: "duration string env", env: "30s", wantErr: "parsing " + envIdleTimeout},
		{name: "fractional env", env: "7.5", wantErr: "parsing " + envIdleTimeout},
		{name: "below minimum env", env: "1", wantErr: "idle timeout 1s is invalid"},
		{name: "below minimum", option: time.Second, wantErr: "idle timeout 1s is invalid"},
		{name: "negative", option: -time.Minute, wantErr: "idle timeout -1m0s is invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := newTestDeps()
			deps.getenv = func(k string) string {
				switch k {
				case envTaskQueue:
					return "test-queue"
				case envIdleTimeout:
					return tc.env
				}
				return ""
			}
			deps.startLambda = func(any, ...lambda.Option) {
				t.Error("Lambda should not start with an invalid idle timeout")
			}

			err := runWorkerInternal(testVersion, func(ctx *Options) error {
				if tc.option != 0 {
					ctx.IdleTimeout = tc.option
				}
				return nil
			}, deps)

			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
