package lambdaworker

import (
	"testing"
	"time"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type mockWorker struct {
	mock.Mock
}

var _ worker.Worker = (*mockWorker)(nil)

func (m *mockWorker) RegisterWorkflow(w interface{}) {
	m.Called(w)
}

func (m *mockWorker) RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions) {
	m.Called(w, options)
}

func (m *mockWorker) RegisterDynamicWorkflow(w interface{}, options workflow.DynamicRegisterOptions) {
	m.Called(w, options)
}

func (m *mockWorker) RegisterActivity(a interface{}) {
	m.Called(a)
}

func (m *mockWorker) RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions) {
	m.Called(a, options)
}

func (m *mockWorker) RegisterDynamicActivity(a interface{}, options activity.DynamicRegisterOptions) {
	m.Called(a, options)
}

func (m *mockWorker) RegisterNexusService(s *nexus.Service) {
	m.Called(s)
}

func (m *mockWorker) Start() error {
	args := m.Called()
	return args.Error(0)
}

func (m *mockWorker) Run(interruptCh <-chan interface{}) error {
	args := m.Called(interruptCh)
	if interruptCh != nil {
		<-interruptCh
	}
	return args.Error(0)
}

func (m *mockWorker) Stop() {
	m.Called()
}

func TestConfigureWorkerContext_SetTaskQueue(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	assert.Equal(t, "", ctx.TaskQueue())
	ctx.SetTaskQueue("my-queue")
	assert.Equal(t, "my-queue", ctx.TaskQueue())
}

func TestConfigureWorkerContext_MutateClientOptions(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	ctx.MutateClientOptions(func(opts *client.Options) error {
		opts.Namespace = "custom-ns"
		return nil
	})

	var opts client.Options
	err := ctx.mutateClientOpts(&opts)
	assert.NoError(t, err)
	assert.Equal(t, "custom-ns", opts.Namespace)
}

func TestConfigureWorkerContext_MutateWorkerOptions(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	ctx.MutateWorkerOptions(func(opts *worker.Options) error {
		opts.MaxConcurrentActivityExecutionSize = 42
		return nil
	})

	var opts worker.Options
	require.NoError(t, ctx.mutateWorkerOpts(&opts))
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
	ctx.RegisterDynamicWorkflow(myWorkflow, workflow.DynamicRegisterOptions{})
	ctx.RegisterDynamicActivity(myActivity, activity.DynamicRegisterOptions{})

	assert.Len(t, ctx.registrations, 6)

	w := &mockWorker{}
	w.On("RegisterWorkflow", mock.Anything).Once()
	w.On("RegisterWorkflowWithOptions", mock.Anything, workflow.RegisterOptions{Name: "custom-wf"}).Once()
	w.On("RegisterActivity", mock.Anything).Once()
	w.On("RegisterActivityWithOptions", mock.Anything, activity.RegisterOptions{Name: "custom-act"}).Once()
	w.On("RegisterDynamicWorkflow", mock.Anything, workflow.DynamicRegisterOptions{}).Once()
	w.On("RegisterDynamicActivity", mock.Anything, activity.DynamicRegisterOptions{}).Once()

	ctx.replayRegistrations(w)
	w.AssertExpectations(t)
}

func TestConfigureWorkerContext_SetShutdownDeadlineBuffer(t *testing.T) {
	ctx := &ConfigureWorkerContext{}
	require.NoError(t, ctx.SetShutdownDeadlineBuffer(5*time.Second))
	assert.Equal(t, 5*time.Second, ctx.shutdownDeadlineBuffer)

	require.NoError(t, ctx.SetShutdownDeadlineBuffer(0))
	assert.Equal(t, time.Duration(0), ctx.shutdownDeadlineBuffer)

	err := ctx.SetShutdownDeadlineBuffer(-1 * time.Second)
	assert.ErrorContains(t, err, "must not be negative")
}
