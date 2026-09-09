package test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// testEnvPlugin records the worker-plugin hooks the test environments invoke,
// exercised here purely through the public API.
type testEnvPlugin struct {
	worker.PluginBase
	startErr             error
	taskQueue            string
	configureKey         string
	startKey             string
	stopKey              string
	starts               int
	stops                int
	registeredActivities []string
}

func (*testEnvPlugin) Name() string { return "test-env-plugin" }

func (p *testEnvPlugin) ConfigureWorker(_ context.Context, options worker.PluginConfigureWorkerOptions) error {
	p.configureKey = options.WorkerInstanceKey
	p.taskQueue = options.TaskQueue
	options.WorkerRegistryOptions.OnRegisterActivity = func(_ any, o activity.RegisterOptions) {
		p.registeredActivities = append(p.registeredActivities, o.Name)
	}
	return nil
}

func (p *testEnvPlugin) StartWorker(
	ctx context.Context,
	options worker.PluginStartWorkerOptions,
	next func(context.Context, worker.PluginStartWorkerOptions) error,
) error {
	p.starts++
	p.startKey = options.WorkerInstanceKey
	if p.startErr != nil {
		return p.startErr
	}
	options.WorkerRegistry.RegisterActivityWithOptions(pluginProvidedActivity, activity.RegisterOptions{Name: "PluginProvidedActivity"})
	return next(ctx, options)
}

func (p *testEnvPlugin) StopWorker(
	ctx context.Context,
	options worker.PluginStopWorkerOptions,
	next func(context.Context, worker.PluginStopWorkerOptions),
) {
	p.stops++
	p.stopKey = options.WorkerInstanceKey
	next(ctx, options)
}

func pluginProvidedActivity(_ context.Context, name string) (string, error) {
	return "hello " + name, nil
}

// pluginConsumerWorkflow calls the plugin-provided activity by name, so it only
// succeeds if the plugin's StartWorker registered it with the environment.
func pluginConsumerWorkflow(ctx workflow.Context, name string) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var out string
	if err := workflow.ExecuteActivity(ctx, "PluginProvidedActivity", name).Get(ctx, &out); err != nil {
		return "", err
	}
	return out, nil
}

func TestTestEnvWorkerPlugin(t *testing.T) {
	t.Run("WorkflowEnvironment", func(t *testing.T) {
		plugin := &testEnvPlugin{}
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})
		env.RegisterWorkflow(pluginConsumerWorkflow)

		env.ExecuteWorkflow(pluginConsumerWorkflow, "temporal")
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var out string
		require.NoError(t, env.GetWorkflowResult(&out))
		require.Equal(t, "hello temporal", out)

		require.Equal(t, "default-test-taskqueue", plugin.taskQueue)
		require.Equal(t, 1, plugin.starts)
		require.Equal(t, 1, plugin.stops)
		require.NotEmpty(t, plugin.configureKey)
		require.Equal(t, plugin.configureKey, plugin.startKey)
		require.Equal(t, plugin.configureKey, plugin.stopKey)
		require.Equal(t, []string{"PluginProvidedActivity"}, plugin.registeredActivities)
	})

	t.Run("ActivityEnvironment", func(t *testing.T) {
		plugin := &testEnvPlugin{}
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})

		// Each ExecuteActivity is one worker run.
		for i := 1; i <= 2; i++ {
			val, err := env.ExecuteActivity("PluginProvidedActivity", "temporal")
			require.NoError(t, err)
			var out string
			require.NoError(t, val.Get(&out))
			require.Equal(t, "hello temporal", out)
			require.Equal(t, i, plugin.starts)
			require.Equal(t, i, plugin.stops)
		}
	})

	t.Run("StartWorkerError", func(t *testing.T) {
		plugin := &testEnvPlugin{startErr: errors.New("start failed")}
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestActivityEnvironment()
		env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})
		_, err := env.ExecuteActivity("PluginProvidedActivity", "temporal")
		require.ErrorIs(t, err, plugin.startErr)
		require.Equal(t, 0, plugin.stops)
	})

	t.Run("SimplePlugin", func(t *testing.T) {
		var before, after int
		plugin, err := temporal.NewSimplePlugin(temporal.SimplePluginOptions{
			Name: "simple-test-env-plugin",
			RunContextBefore: func(_ context.Context, o temporal.SimplePluginRunContextBeforeOptions) error {
				before++
				o.Registry.RegisterActivityWithOptions(pluginProvidedActivity, activity.RegisterOptions{Name: "PluginProvidedActivity"})
				return nil
			},
			RunContextAfter: func(context.Context, temporal.SimplePluginRunContextAfterOptions) { after++ },
		})
		require.NoError(t, err)

		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.SetWorkerOptions(worker.Options{Plugins: []worker.Plugin{plugin}})
		env.RegisterWorkflow(pluginConsumerWorkflow)
		env.ExecuteWorkflow(pluginConsumerWorkflow, "temporal")
		require.NoError(t, env.GetWorkflowError())
		require.Equal(t, 1, before)
		require.Equal(t, 1, after)
	})
}
