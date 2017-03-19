package cadence

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/uber-common/bark"

	m "github.com/uber-go/cadence-client/.gen/go/cadence"
)

// ** This is for internal stress testing framework **

// PressurePoints
const (
	PressurePointTypeDecisionTaskStartTimeout    = "decision-task-start-timeout"
	PressurePointTypeDecisionTaskCompleted       = "decision-task-complete"
	PressurePointTypeActivityTaskScheduleTimeout = "activity-task-schedule-timeout"
	PressurePointTypeActivityTaskStartTimeout    = "activity-task-start-timeout"
	PressurePointConfigProbability               = "probability"
	PressurePointConfigSleep                     = "sleep"
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
	factory WorkflowFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	pressurePoints map[string]map[string]string) (worker Lifecycle) {
	return newWorkflowWorker(
		func(workflowType WorkflowType) (workflowDefinition, error) {
			wd, err := factory(workflowType)
			if err != nil {
				return nil, err
			}
			return NewWorkflowDefinition(wd), nil
		},
		service,
		params,
		&pressurePointMgrImpl{config: pressurePoints, logger: params.Logger})
}

func (p *pressurePointMgrImpl) Execute(pressurePointName string) error {
	if config, ok := p.config[pressurePointName]; ok {
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
		} else if value, ok3 := config[PressurePointConfigSleep]; ok3 {
			if timeout, err := strconv.Atoi(value); err == nil {
				if timeout > 0 {
					p.logger.Debugf("Execute: PressurePointName: %s, Sleep for: %d.",
						pressurePointName, timeout)
					d := time.Duration(timeout) * time.Second
					time.Sleep(d)
					return nil
				}
			}
		}
	}
	return nil
}
