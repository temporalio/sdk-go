package cadence

import (
	"errors"
	"math/rand"
	"strconv"
	"time"

	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	"go.uber.org/zap"
)

// ** This is for internal stress testing framework **

// PressurePoints
const (
	pressurePointTypeDecisionTaskStartTimeout    = "decision-task-start-timeout"
	pressurePointTypeDecisionTaskCompleted       = "decision-task-complete"
	pressurePointTypeActivityTaskScheduleTimeout = "activity-task-schedule-timeout"
	pressurePointTypeActivityTaskStartTimeout    = "activity-task-start-timeout"
	pressurePointConfigProbability               = "probability"
	pressurePointConfigSleep                     = "sleep"
	workerOptionsConfig                          = "worker-options"
	workerOptionsConfigConcurrentPollRoutineSize = "ConcurrentPollRoutineSize"
)

type (
	pressurePointMgr interface {
		Execute(pressurePointName string) error
	}

	pressurePointMgrImpl struct {
		config map[string]map[string]string
		logger *zap.Logger
	}
)

// newWorkflowWorkerWithPressurePoints returns an instance of a workflow worker.
func newWorkflowWorkerWithPressurePoints(
	factory workflowFactory,
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	pressurePoints map[string]map[string]string) (worker Worker) {
	return newWorkflowWorker(
		func(workflowType WorkflowType) (workflowDefinition, error) {
			wd, err := factory(workflowType)
			if err != nil {
				return nil, err
			}
			return newWorkflowDefinition(wd), nil
		},
		service,
		domain,
		params,
		&pressurePointMgrImpl{config: pressurePoints, logger: params.Logger})
}

func (p *pressurePointMgrImpl) Execute(pressurePointName string) error {
	if config, ok := p.config[pressurePointName]; ok {
		// If probability is configured.
		if value, ok2 := config[pressurePointConfigProbability]; ok2 {
			if probablity, err := strconv.Atoi(value); err == nil {
				if rand.Int31n(100) < int32(probablity) {
					// Drop the task.
					p.logger.Debug("pressurePointMgrImpl.Execute drop task.",
						zap.String("PressurePointName", pressurePointName),
						zap.Int("probability", probablity))
					return errors.New("pressurepoint configured")
				}
			}
		} else if value, ok3 := config[pressurePointConfigSleep]; ok3 {
			if timeout, err := strconv.Atoi(value); err == nil {
				if timeout > 0 {
					p.logger.Debug("pressurePointMgrImpl.Execute sleep.",
						zap.String("PressurePointName", pressurePointName),
						zap.Int("DurationSeconds", timeout))
					d := time.Duration(timeout) * time.Second
					time.Sleep(d)
					return nil
				}
			}
		}
	}
	return nil
}
