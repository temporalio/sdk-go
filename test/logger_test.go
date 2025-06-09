//go:build go1.21

package test_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func logWorkflow(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("Logging from workflow", "name", name)

	var result interface{}
	err := workflow.ExecuteActivity(ctx, loggingActivity, name).Get(ctx, &result)
	if err != nil {
		logger.Error("LoggingActivity failed.", "Error", err)
		return err
	}

	err = workflow.ExecuteActivity(ctx, loggingErrorActivity).Get(ctx, &result)
	if err != nil {
		logger.Error("LoggingActivity failed.", "Error", err)
		return err
	}

	logger.Info("Workflow completed.")
	return nil
}

func loggingActivity(ctx context.Context, name string) error {
	logger := activity.GetLogger(ctx)
	withLogger := logger.(log.WithLogger).With("activity", "LoggingActivity")

	withLogger.Info("Executing LoggingActivity.", "name", name)
	withLogger.Debug("Debugging LoggingActivity.", "value", "important debug data")
	return nil
}

func loggingErrorActivity(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Warn("Ignore next error message. It is just for demo purpose.")
	logger.Error("Unable to execute LoggingErrorAcctivity.", "error", errors.New("random error"))
	return nil
}

func Test_StructuredLogger(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}

	var buf bytes.Buffer
	th := slog.NewJSONHandler(&buf, &slog.HandlerOptions{AddSource: true, Level: slog.LevelDebug})
	testSuite.SetLogger(log.NewStructuredLogger(slog.New(th)))
	env := testSuite.NewTestWorkflowEnvironment()

	// Mock activity implementation
	env.RegisterActivity(loggingActivity)
	env.RegisterActivity(loggingErrorActivity)

	env.ExecuteWorkflow(logWorkflow, "Temporal")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, env.GetWorkflowResult(nil))

	// Parse logs
	var ms []map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		err := json.Unmarshal(line, &m)
		require.NoError(t, err)
		fmt.Println(m)
		ms = append(ms, m)
	}

	expectedLogs := []slog.Source{
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.logWorkflow",
		},
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.loggingActivity",
		},
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.loggingActivity",
		},
		{
			File:     "internal_workflow_testsuite.go",
			Function: "go.temporal.io/sdk/internal.(*testWorkflowEnvironmentImpl).handleActivityResult",
		},
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.loggingErrorActivity",
		},
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.loggingErrorActivity",
		},
		{
			File:     "internal_workflow_testsuite.go",
			Function: "go.temporal.io/sdk/internal.(*testWorkflowEnvironmentImpl).handleActivityResult",
		},
		{
			File:     "logger_test.go",
			Function: "go.temporal.io/sdk/test_test.logWorkflow",
		},
	}

	require.Equal(t, len(expectedLogs), len(ms))
	for i, expectedLog := range expectedLogs {
		fmt.Println(i)
		actualSource := ms[i][slog.SourceKey].(map[string]any)
		require.True(t, strings.HasSuffix(actualSource["file"].(string), expectedLog.File))
		require.Equal(t, expectedLog.Function, actualSource["function"])
		// Skip line to make the test less annoying to maintain
	}
}
