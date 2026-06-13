package lambdaworker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

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
	var opts client.Options
	applyLambdaClientDefaults(&opts)

	assert.NotNil(t, opts.Logger)
	// Identity is not set during init; it's set from the Lambda invocation context.
	assert.Empty(t, opts.Identity)
}

func TestApplyLambdaClientDefaults_PreservesExistingLogger(t *testing.T) {
	// Get a reference logger by applying defaults to a zero-value options.
	var refOpts client.Options
	applyLambdaClientDefaults(&refOpts)
	originalLogger := refOpts.Logger

	// Applying defaults again should preserve the existing logger.
	opts := client.Options{Logger: originalLogger}
	applyLambdaClientDefaults(&opts)

	assert.Equal(t, originalLogger, opts.Logger)
}

func TestBuildLambdaIdentity(t *testing.T) {
	tests := []struct {
		name        string
		requestID   string
		functionARN string
		expected    string
	}{
		{
			name:        "both set",
			requestID:   "req-abc-123",
			functionARN: "arn:aws:lambda:us-east-1:123456:function:my-func",
			expected:    "req-abc-123@arn:aws:lambda:us-east-1:123456:function:my-func",
		},
		{
			name:        "empty request ID",
			requestID:   "",
			functionARN: "arn:aws:lambda:us-east-1:123456:function:my-func",
			expected:    "unknown@arn:aws:lambda:us-east-1:123456:function:my-func",
		},
		{
			name:        "empty function ARN",
			requestID:   "req-abc-123",
			functionARN: "",
			expected:    "req-abc-123@unknown",
		},
		{
			name:        "both empty",
			requestID:   "",
			functionARN: "",
			expected:    "unknown@unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := buildLambdaIdentity(tt.requestID, tt.functionARN)
			assert.Equal(t, tt.expected, identity)
		})
	}
}

func TestLambdaDefaultConfigFilePath(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		expected string
	}{
		{
			name:     "TEMPORAL_CONFIG_FILE takes priority",
			env:      map[string]string{"TEMPORAL_CONFIG_FILE": "/custom/path.toml", "LAMBDA_TASK_ROOT": "/var/task"},
			expected: "/custom/path.toml",
		},
		{
			name:     "falls back to LAMBDA_TASK_ROOT",
			env:      map[string]string{"LAMBDA_TASK_ROOT": "/var/task"},
			expected: "/var/task/temporal.toml",
		},
		{
			name:     "falls back to current directory",
			env:      map[string]string{},
			expected: "temporal.toml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			assert.Equal(t, tt.expected, lambdaDefaultConfigFilePath(getenv))
		})
	}
}
