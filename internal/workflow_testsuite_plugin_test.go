package internal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
)

// envPluginForTest records every worker-plugin hook the test environments
// invoke so tests can assert the lifecycle mirrors a real worker.
type envPluginForTest struct {
	WorkerPluginBase
	interceptor          *tracingWorkerInterceptor
	configureErr         error
	startErr             error
	configureKeys        []string
	startKeys            []string
	stopKeys             []string
	taskQueues           []string
	workflowCallbacks    int
	registeredActivities []string
}

func (p *envPluginForTest) Name() string { return "env-plugin-for-test" }

func (p *envPluginForTest) ConfigureWorker(_ context.Context, options WorkerPluginConfigureWorkerOptions) error {
	p.configureKeys = append(p.configureKeys, options.WorkerInstanceKey)
	p.taskQueues = append(p.taskQueues, options.TaskQueue)
	if p.configureErr != nil {
		return p.configureErr
	}
	if p.interceptor != nil {
		options.WorkerOptions.Interceptors = append(options.WorkerOptions.Interceptors, p.interceptor)
	}
	options.WorkerRegistryOptions.OnRegisterWorkflow = func(any, RegisterWorkflowOptions) {
		p.workflowCallbacks++
	}
	options.WorkerRegistryOptions.OnRegisterActivity = func(_ any, o RegisterActivityOptions) {
		p.registeredActivities = append(p.registeredActivities, o.Name)
	}
	return nil
}

func (p *envPluginForTest) StartWorker(
	ctx context.Context,
	options WorkerPluginStartWorkerOptions,
	next func(context.Context, WorkerPluginStartWorkerOptions) error,
) error {
	p.startKeys = append(p.startKeys, options.WorkerInstanceKey)
	if p.startErr != nil {
		return p.startErr
	}
	options.WorkerRegistry.RegisterActivityWithOptions(envPluginActivity, RegisterActivityOptions{Name: envPluginActivityName})
	return next(ctx, options)
}

func (p *envPluginForTest) StopWorker(
	ctx context.Context,
	options WorkerPluginStopWorkerOptions,
	next func(context.Context, WorkerPluginStopWorkerOptions),
) {
	p.stopKeys = append(p.stopKeys, options.WorkerInstanceKey)
	next(ctx, options)
}

const envPluginActivityName = "EnvPluginActivity"

func envPluginActivity(_ context.Context, name string) (string, error) {
	if name == "pending" {
		return "", ErrActivityResultPending
	}
	return "hello " + name, nil
}

// envPluginWorkflow calls the plugin-provided activity by name, so it only
// succeeds if the plugin's StartWorker registered it.
func envPluginWorkflow(ctx Context, name string) (string, error) {
	ctx = WithActivityOptions(ctx, ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	var out string
	if err := ExecuteActivity(ctx, envPluginActivityName, name).Get(ctx, &out); err != nil {
		return "", err
	}
	return out, nil
}

func envPluginParentWorkflow(ctx Context, name string) (string, error) {
	var out string
	if err := ExecuteChildWorkflow(ctx, envPluginWorkflow, name).Get(ctx, &out); err != nil {
		return "", err
	}
	return out, nil
}

func envPluginFailingWorkflow(Context) error {
	return errors.New("workflow failed")
}

func newEnvPluginWorkflowEnv(plugin WorkerPlugin) *TestWorkflowEnvironment {
	env := (&WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}})
	return env
}

func TestWorkflowEnvPluginLifecycle(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{interceptor: &tracingWorkerInterceptor{}}
	env := newEnvPluginWorkflowEnv(plugin)
	// ConfigureWorker ran at SetWorkerOptions with the default task queue; the
	// worker has not started yet.
	require.Equal(t, []string{defaultTestTaskQueue}, plugin.taskQueues)
	require.Empty(t, plugin.startKeys)

	env.RegisterWorkflow(envPluginWorkflow)
	env.ExecuteWorkflow(envPluginWorkflow, "temporal")
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "hello temporal", out)

	// Interceptor appended in ConfigureWorker was applied to the workflow.
	require.Len(t, plugin.interceptor.instances, 1)
	require.Contains(t, plugin.interceptor.instances[0].trace, "ExecuteActivity "+envPluginActivityName)

	// Callbacks: the explicit RegisterWorkflow fired once (the workflow passed
	// to ExecuteWorkflow does not), and the plugin's own activity registration
	// went through the callback too.
	require.Equal(t, 1, plugin.workflowCallbacks)
	require.Equal(t, []string{envPluginActivityName}, plugin.registeredActivities)

	// One start, one stop, same instance key throughout.
	require.Len(t, plugin.configureKeys, 1)
	require.NotEmpty(t, plugin.configureKeys[0])
	require.Equal(t, plugin.configureKeys, plugin.startKeys)
	require.Equal(t, plugin.configureKeys, plugin.stopKeys)

	// A second ExecuteWorkflow on the same environment still panics as before
	// and does not run another start/stop pair.
	require.Panics(t, func() { env.ExecuteWorkflow(envPluginWorkflow, "again") })
	require.Len(t, plugin.startKeys, 1)
	require.Len(t, plugin.stopKeys, 1)
}

func TestWorkflowEnvPluginChildWorkflow(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{}
	env := newEnvPluginWorkflowEnv(plugin)
	env.RegisterWorkflow(envPluginParentWorkflow)
	env.RegisterWorkflow(envPluginWorkflow)
	env.ExecuteWorkflow(envPluginParentWorkflow, "temporal")
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	require.Equal(t, "hello temporal", out)

	// The child shares the parent's worker: exactly one start and one stop.
	require.Len(t, plugin.startKeys, 1)
	require.Len(t, plugin.stopKeys, 1)
}

func TestWorkflowEnvPluginWorkflowError(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{}
	env := newEnvPluginWorkflowEnv(plugin)
	env.RegisterWorkflow(envPluginFailingWorkflow)
	env.ExecuteWorkflow(envPluginFailingWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.ErrorContains(t, env.GetWorkflowError(), "workflow failed")
	require.Len(t, plugin.stopKeys, 1)
}

func TestWorkflowEnvPluginStartError(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{startErr: errors.New("start failed")}
	env := newEnvPluginWorkflowEnv(plugin)
	env.RegisterWorkflow(envPluginWorkflow)
	require.PanicsWithError(t, "start failed", func() { env.ExecuteWorkflow(envPluginWorkflow, "temporal") })
	require.Len(t, plugin.startKeys, 1)
	// A worker whose start failed is not stopped.
	require.Empty(t, plugin.stopKeys)
}

func TestWorkflowEnvPluginConfigureError(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{configureErr: errors.New("configure failed")}
	env := (&WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	require.PanicsWithError(t, "configure failed", func() {
		env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}})
	})

	// Plugins may only be configured once per environment, like worker.New.
	env = newEnvPluginWorkflowEnv(&envPluginForTest{})
	require.PanicsWithValue(t, "SetWorkerOptions may not be called again after Plugins were configured", func() {
		env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{&envPluginForTest{}}})
	})
	require.PanicsWithValue(t, "SetWorkerOptions may not be called again after Plugins were configured", func() {
		env.SetWorkerOptions(WorkerOptions{})
	})

	// A failed ConfigureWorker does not poison the environment: the plugins are
	// not adopted, so the environment can still be configured afterwards.
	env = (&WorkflowTestSuite{}).NewTestWorkflowEnvironment()
	require.Panics(t, func() { env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}}) })
	env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{&envPluginForTest{}}})
}

func TestWorkflowEnvPluginStopsOnExecutionPanic(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{}
	env := newEnvPluginWorkflowEnv(plugin)
	// Executing an unregistered workflow type panics inside ExecuteWorkflow;
	// the worker that was started for it is still stopped.
	require.Panics(t, func() { env.ExecuteWorkflow("no-such-workflow") })
	require.Len(t, plugin.startKeys, 1)
	require.Len(t, plugin.stopKeys, 1)
}

// envPluginRegistryCallbacks records every registry callback a plugin can set.
type envPluginRegistryCallbacks struct {
	WorkerPluginBase
	activities        []RegisterActivityOptions
	dynamicActivities int
	dynamicWorkflows  []DynamicRegisterWorkflowOptions
	nexusServices     []string
}

func (*envPluginRegistryCallbacks) Name() string { return "env-plugin-registry-callbacks" }

func (p *envPluginRegistryCallbacks) ConfigureWorker(_ context.Context, options WorkerPluginConfigureWorkerOptions) error {
	options.WorkerRegistryOptions.OnRegisterActivity = func(_ any, o RegisterActivityOptions) {
		p.activities = append(p.activities, o)
	}
	options.WorkerRegistryOptions.OnRegisterDynamicActivity = func(any, DynamicRegisterActivityOptions) {
		p.dynamicActivities++
	}
	options.WorkerRegistryOptions.OnRegisterDynamicWorkflow = func(_ any, o DynamicRegisterWorkflowOptions) {
		p.dynamicWorkflows = append(p.dynamicWorkflows, o)
	}
	options.WorkerRegistryOptions.OnRegisterNexusService = func(s *nexus.Service) {
		p.nexusServices = append(p.nexusServices, s.Name)
	}
	return nil
}

func envPluginDynamicWorkflow(Context, converter.EncodedValues) (converter.EncodedValues, error) {
	return nil, nil
}

func envPluginDynamicActivity(context.Context, converter.EncodedValues) (converter.EncodedValues, error) {
	return nil, nil
}

func TestWorkflowEnvPluginRegistryCallbacks(t *testing.T) {
	t.Parallel()
	plugin := &envPluginRegistryCallbacks{}
	env := newEnvPluginWorkflowEnv(plugin)

	env.RegisterActivity(envPluginActivity)
	env.RegisterActivityWithOptions(envPluginActivity, RegisterActivityOptions{Name: "Named"})
	env.RegisterDynamicActivity(envPluginDynamicActivity, DynamicRegisterActivityOptions{})
	loadOptions := func(LoadDynamicRuntimeOptionsDetails) (DynamicRuntimeWorkflowOptions, error) {
		return DynamicRuntimeWorkflowOptions{}, nil
	}
	env.RegisterDynamicWorkflow(envPluginDynamicWorkflow, DynamicRegisterWorkflowOptions{LoadDynamicRuntimeOptions: loadOptions})
	env.RegisterNexusService(nexus.NewService("env-plugin-service"))

	// Callbacks see the caller's options, as on a real worker: RegisterActivity
	// reports zero options and the relaxed duplicate check is not visible.
	require.Equal(t, []RegisterActivityOptions{{}, {Name: "Named"}}, plugin.activities)
	require.Equal(t, 1, plugin.dynamicActivities)
	require.Len(t, plugin.dynamicWorkflows, 1)
	require.NotNil(t, plugin.dynamicWorkflows[0].LoadDynamicRuntimeOptions)
	require.Equal(t, []string{"env-plugin-service"}, plugin.nexusServices)
}

func TestActivityEnvPluginStopsOnPendingResult(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{}
	env := (&WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}})
	// A pending (asynchronously completed) result still ends this execution's
	// worker run.
	_, err := env.ExecuteActivity(envPluginActivityName, "pending")
	require.ErrorIs(t, err, ErrActivityResultPending)
	require.Len(t, plugin.stopKeys, 1)
}

func TestActivityEnvPluginLifecycle(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{}
	env := (&WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}})
	require.Equal(t, []string{defaultTestTaskQueue}, plugin.taskQueues)

	// Each execution is one worker run: the plugin registers the activity in
	// StartWorker every time and is stopped when the call returns.
	for i := 1; i <= 2; i++ {
		val, err := env.ExecuteActivity(envPluginActivityName, "temporal")
		require.NoError(t, err)
		var out string
		require.NoError(t, val.Get(&out))
		require.Equal(t, "hello temporal", out)
		require.Len(t, plugin.startKeys, i)
		require.Len(t, plugin.stopKeys, i)
	}

	val, err := env.ExecuteLocalActivity(envPluginActivity, "local")
	require.NoError(t, err)
	var out string
	require.NoError(t, val.Get(&out))
	require.Equal(t, "hello local", out)
	require.Len(t, plugin.startKeys, 3)
	require.Len(t, plugin.stopKeys, 3)

	// The same instance key is used throughout the environment's life.
	for _, key := range append(plugin.startKeys, plugin.stopKeys...) {
		require.Equal(t, plugin.configureKeys[0], key)
	}
}

func TestActivityEnvPluginStartError(t *testing.T) {
	t.Parallel()
	plugin := &envPluginForTest{startErr: errors.New("start failed")}
	env := (&WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.SetWorkerOptions(WorkerOptions{Plugins: []WorkerPlugin{plugin}})

	// The activity environment returns the start error instead of panicking,
	// and does not stop a worker that never started.
	_, err := env.ExecuteActivity(envPluginActivityName, "temporal")
	require.ErrorIs(t, err, plugin.startErr)
	_, err = env.ExecuteLocalActivity(envPluginActivity, "temporal")
	require.ErrorIs(t, err, plugin.startErr)
	require.Len(t, plugin.startKeys, 2)
	require.Empty(t, plugin.stopKeys)
}
