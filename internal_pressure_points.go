// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cadence

import (
	"errors"
	"math/rand"
	"strconv"
	"time"

	m "go.uber.org/cadence/.gen/go/cadence"
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
			if probability, err := strconv.Atoi(value); err == nil {
				if rand.Int31n(100) < int32(probability) {
					// Drop the task.
					p.logger.Debug("pressurePointMgrImpl.Execute drop task.",
						zap.String("PressurePointName", pressurePointName),
						zap.Int("probability", probability))
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
