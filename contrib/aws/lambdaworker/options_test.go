package lambdaworker

import (
	"testing"

	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"go.temporal.io/sdk/activity"
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

func myWorkflow()  {}
func myActivity()  {}
func myActivity2() {}

func TestConfigureWorkerContext_ReplayRegistrations(t *testing.T) {
	ctx := &Options{}
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
