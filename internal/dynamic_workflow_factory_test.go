package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

// --- Site 1: production registry.getWorkflowDefinition ---

// factoryBackedDefinition is a sentinel WorkflowDefinition used to assert, by
// type identity, that the dynamic dispatch path resolved the registered
// WorkflowDefinitionFactory. Embedding the interface satisfies it without a
// real coroutine implementation.
type factoryBackedDefinition struct {
	WorkflowDefinition
}

type sentinelDynamicFactory struct{}

func (sentinelDynamicFactory) NewWorkflowDefinition() WorkflowDefinition {
	return &factoryBackedDefinition{}
}

// A dynamic workflow registered as a WorkflowDefinitionFactory must execute via
// NewWorkflowDefinition(). Before the fix, getWorkflowDefinition wrapped the
// factory value in a workflowExecutor and reflect-called it, panicking with
// "reflect: call of reflect.Value.Call on ptr Value".
func TestGetWorkflowDefinition_DynamicWorkflowFactory(t *testing.T) {
	r := newRegistry()
	r.RegisterDynamicWorkflow(sentinelDynamicFactory{}, DynamicRegisterWorkflowOptions{})

	def, err := r.getWorkflowDefinition(WorkflowType{Name: "some-unregistered-type"})
	require.NoError(t, err)
	require.IsType(t, &factoryBackedDefinition{}, def,
		"a factory-registered dynamic workflow must be executed via NewWorkflowDefinition()")
}

// --- Site 2: test environment getWorkflowDefinition ---

// echoDynamicDefinition is a minimal WorkflowDefinition that completes
// immediately, echoing the (dynamically dispatched) workflow type name.
type echoDynamicDefinition struct{ env WorkflowEnvironment }

func (d *echoDynamicDefinition) Execute(env WorkflowEnvironment, _ *commonpb.Header, _ *commonpb.Payloads) {
	d.env = env
}

func (d *echoDynamicDefinition) OnWorkflowTaskStarted(_ time.Duration) {
	name := d.env.WorkflowInfo().WorkflowType.Name
	result, err := converter.GetDefaultDataConverter().ToPayloads(name)
	d.env.Complete(result, err)
}

func (d *echoDynamicDefinition) StackTrace() string { return "" }
func (d *echoDynamicDefinition) Close()             {}

type echoDynamicFactory struct{}

func (echoDynamicFactory) NewWorkflowDefinition() WorkflowDefinition {
	return &echoDynamicDefinition{}
}

// The same fix is required in the test environment: a factory-registered
// dynamic workflow must execute rather than panic.
func TestDynamicWorkflowFactoryInTestEnvironment(t *testing.T) {
	s := WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterDynamicWorkflow(echoDynamicFactory{}, DynamicRegisterWorkflowOptions{})

	env.ExecuteWorkflow("pipeline-blog-publish")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var got string
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, "pipeline-blog-publish", got)
}

func dynamicFactoryParentWorkflow(ctx Context) (string, error) {
	child := ExecuteChildWorkflow(ctx, "dynamic-child")
	var execution WorkflowExecution
	if err := child.GetChildWorkflowExecution().Get(ctx, &execution); err != nil {
		return "", err
	}
	return execution.ID, nil
}

func TestDynamicWorkflowFactoryChildInTestEnvironment(t *testing.T) {
	s := WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(dynamicFactoryParentWorkflow)
	env.RegisterDynamicWorkflow(echoDynamicFactory{}, DynamicRegisterWorkflowOptions{})

	env.ExecuteWorkflow(dynamicFactoryParentWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var childWorkflowID string
	require.NoError(t, env.GetWorkflowResult(&childWorkflowID))
	require.NotEmpty(t, childWorkflowID)
}

type updateDrainingDefinition struct {
	env     WorkflowEnvironment
	drained bool
}

func (d *updateDrainingDefinition) Execute(env WorkflowEnvironment, _ *commonpb.Header, _ *commonpb.Payloads) {
	d.env = env
	env.QueueUpdate("unhandled", func() {
		d.drained = true
	})
}

func (d *updateDrainingDefinition) OnWorkflowTaskStarted(_ time.Duration) {
	drained := d.env.DrainUnhandledUpdates()
	result, err := converter.GetDefaultDataConverter().ToPayloads(drained && d.drained)
	d.env.Complete(result, err)
}

func (d *updateDrainingDefinition) StackTrace() string { return "" }
func (d *updateDrainingDefinition) Close()             {}

type updateDrainingFactory struct{}

func (updateDrainingFactory) NewWorkflowDefinition() WorkflowDefinition {
	return &updateDrainingDefinition{}
}

func TestDynamicWorkflowFactoryDrainsUnhandledUpdatesInTestEnvironment(t *testing.T) {
	s := WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	env.RegisterDynamicWorkflow(updateDrainingFactory{}, DynamicRegisterWorkflowOptions{})

	env.ExecuteWorkflow("dynamic-update-draining")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var drained bool
	require.NoError(t, env.GetWorkflowResult(&drained))
	require.True(t, drained)
}
