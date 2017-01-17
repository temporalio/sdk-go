package cadence

import (
	"fmt"
	"math/rand"
	"strconv"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
	"code.uber.internal/devexp/minions-client-go.git/common/metrics"
	"github.com/uber-common/bark"
)

// ** This is for internal stress testing framework **

// PressurePoints
const (
	PressurePointTypeDecisionTaskStartTimeout    = "decision-task-start-timeout"
	PressurePointTypeActivityTaskScheduleTimeout = "activity-task-schedule-timeout"
	PressurePointTypeActivityTaskStartTimeout    = "activity-task-start-timeout"
	PressurePointConfigProbability               = "probability"
)

type (
	pressurePointMgr interface {
		Execute(pressurePointName string) error
	}

	pressurePointMgrImpl struct {
		config map[string]map[string]string
		logger bark.Logger
	}
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
		&pressurePointMgrImpl{config: pressurePoints, logger: logger})
}

func (p *pressurePointMgrImpl) Execute(pressurePointName string) error {
	if config, ok := p.config[PressurePointTypeDecisionTaskStartTimeout]; ok {
		// If probability is configured.
		if value, ok2 := config[PressurePointConfigProbability]; ok2 {
			if probablity, err := strconv.Atoi(value); err == nil {
				if rand.Int31n(100) < int32(probablity) {
					// Drop the task.
					p.logger.Debugf("Execute: PressurePointName: %s, Configured with probability: %d is getting dropped.",
						pressurePointName, probablity)
					return fmt.Errorf("pressurepoint configured")
				}
			}
		}
	}
	return nil
}
