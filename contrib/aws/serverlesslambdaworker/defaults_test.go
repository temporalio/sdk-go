package serverlesslambdaworker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func TestApplyLambdaWorkerDefaults(t *testing.T) {
	var opts worker.Options
	applyLambdaWorkerDefaults(&opts)

	assert.Equal(t, 10, opts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, 10, opts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, 10, opts.MaxConcurrentLocalActivityExecutionSize)
	assert.Equal(t, 5, opts.MaxConcurrentNexusTaskExecutionSize)
	assert.Equal(t, 1, opts.MaxConcurrentActivityTaskPollers)
	assert.Equal(t, 2, opts.MaxConcurrentWorkflowTaskPollers)
	assert.Equal(t, 1, opts.MaxConcurrentNexusTaskPollers)
	assert.Equal(t, 5*time.Second, opts.WorkerStopTimeout)
	assert.True(t, opts.DisableEagerActivities)
}

func TestApplyLambdaWorkerDefaults_PreservesExisting(t *testing.T) {
	opts := worker.Options{
		MaxConcurrentActivityExecutionSize: 50,
		WorkerStopTimeout:                  10 * time.Second,
	}
	applyLambdaWorkerDefaults(&opts)

	// Existing values preserved.
	assert.Equal(t, 50, opts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, 10*time.Second, opts.WorkerStopTimeout)
	// Zero-valued fields get Lambda defaults.
	assert.Equal(t, 10, opts.MaxConcurrentWorkflowTaskExecutionSize)
	// DisableEagerActivities is always set.
	assert.True(t, opts.DisableEagerActivities)
}

func TestApplyLambdaClientDefaults(t *testing.T) {
	env := map[string]string{
		"AWS_LAMBDA_FUNCTION_NAME":    "my-func",
		"AWS_LAMBDA_FUNCTION_VERSION": "$LATEST",
	}
	getenv := func(k string) string { return env[k] }

	var opts client.Options
	applyLambdaClientDefaults(&opts, getenv)

	assert.NotNil(t, opts.Logger)
	assert.Contains(t, opts.Identity, "lambda:my-func:$LATEST@")
}

func TestApplyLambdaClientDefaults_PreservesExisting(t *testing.T) {
	getenv := func(string) string { return "" }

	opts := client.Options{Identity: "custom-identity"}
	applyLambdaClientDefaults(&opts, getenv)

	assert.Equal(t, "custom-identity", opts.Identity)
}

func TestBuildLambdaIdentity(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		contains []string
	}{
		{
			name: "all env vars set",
			env: map[string]string{
				"AWS_LAMBDA_FUNCTION_NAME":    "my-func",
				"AWS_LAMBDA_FUNCTION_VERSION": "42",
			},
			contains: []string{"lambda:", "my-func", "42"},
		},
		{
			name:     "no env vars",
			env:      map[string]string{},
			contains: []string{"lambda:", "unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			identity := buildLambdaIdentity(getenv)
			for _, s := range tt.contains {
				assert.Contains(t, identity, s)
			}
		})
	}
}

func TestResolveTaskQueue(t *testing.T) {
	getenv := func(k string) string {
		if k == "TEMPORAL_TASK_QUEUE" {
			return "env-queue"
		}
		return ""
	}

	// User-set takes priority.
	tq, err := resolveTaskQueue("user-queue", getenv)
	require.NoError(t, err)
	assert.Equal(t, "user-queue", tq)

	// Falls back to env var.
	tq, err = resolveTaskQueue("", getenv)
	require.NoError(t, err)
	assert.Equal(t, "env-queue", tq)

	// Error when neither is set.
	_, err = resolveTaskQueue("", func(string) string { return "" })
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task queue not configured")
}
