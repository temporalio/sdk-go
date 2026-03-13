package serverlesslambdaworker

import (
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// mockWorker implements worker.Worker for testing registration replay.
type mockWorker struct {
	mock.Mock
}

func (m *mockWorker) RegisterWorkflow(w interface{}) {
	m.Called(w)
}

func (m *mockWorker) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	m.Called(w, options)
}

func (m *mockWorker) RegisterActivity(a interface{}) {
	m.Called(a)
}

func (m *mockWorker) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	m.Called(a, options)
}

func (m *mockWorker) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockWorker) Run(<-chan interface{}) error {
	return nil
}

func (m *mockWorker) Stop() {
	m.Called()
}

func (m *mockWorker) RegisterDynamicWorkflow(w interface{}, options workflow.DynamicRegisterOptions) {
	m.Called(w, options)
}

func (m *mockWorker) RegisterDynamicActivity(a interface{}, options activity.DynamicRegisterOptions) {
	m.Called(a, options)
}

func (m *mockWorker) RegisterNexusService(s *nexus.Service) {
	m.Called(s)
}

func TestConfigureWorkerContext_SetTaskQueue(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	assert.Equal(t, "", ctx.TaskQueue())
	ctx.SetTaskQueue("my-queue")
	assert.Equal(t, "my-queue", ctx.TaskQueue())
}

func TestConfigureWorkerContext_MutateClientOptions(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	ctx.MutateClientOptions(func(opts *client.Options) {
		opts.Namespace = "custom-ns"
	})

	var opts client.Options
	ctx.mutateClientOpts(&opts)
	assert.Equal(t, "custom-ns", opts.Namespace)
}

func TestConfigureWorkerContext_MutateWorkerOptions(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	ctx.MutateWorkerOptions(func(opts *worker.Options) {
		opts.MaxConcurrentActivityExecutionSize = 42
	})

	var opts worker.Options
	ctx.mutateWorkerOpts(&opts)
	assert.Equal(t, 42, opts.MaxConcurrentActivityExecutionSize)
}

func myWorkflow()  {}
func myActivity()  {}
func myActivity2() {}

func TestConfigureWorkerContext_ReplayRegistrations(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	ctx.RegisterWorkflow(myWorkflow)
	ctx.RegisterWorkflowWithOptions(myWorkflow, workflow.RegisterOptions{Name: "custom-wf"})
	ctx.RegisterActivity(myActivity)
	ctx.RegisterActivityWithOptions(myActivity2, activity.RegisterOptions{Name: "custom-act"})

	assert.Len(t, ctx.registrations, 4)

	w := &mockWorker{}
	w.On("RegisterWorkflow", mock.Anything).Once()
	w.On("RegisterWorkflowWithOptions", mock.Anything, workflow.RegisterOptions{Name: "custom-wf"}).Once()
	w.On("RegisterActivity", mock.Anything).Once()
	w.On("RegisterActivityWithOptions", mock.Anything, activity.RegisterOptions{Name: "custom-act"}).Once()

	ctx.replayRegistrations(w)
	w.AssertExpectations(t)
}
