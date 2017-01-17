package cadence

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"github.com/uber-common/bark"
)

// ** This is for internal stress testing framework **

// PressurePoints
const (
	PressurePointTypeDecisionTaskStartTimeout = "decision-task-start-timeout"
	PressurePointConfigProbability            = "probability"
)

// NewWorkflowWorkerWithPressurePoints returns an instance of a workflow worker.
func NewWorkflowWorkerWithPressurePoints(
	params WorkerExecutionParameters,
	factory WorkflowFactory,
	service m.TChanWorkflowService,
	logger bark.Logger,
	reporter metrics.Reporter,
	pressurePoints map[string]map[string]string) (worker Lifecycle) {
	return newWorkflowWorker(
		params,
		func(workflowType WorkflowType) (workflowDefinition, Error) {
			wd, err := factory(workflowType)
			if err != nil {
				return nil, err
			}
			return NewWorkflowDefinition(wd), nil
		},
		service,
		logger,
		reporter,
		pressurePoints)
}
