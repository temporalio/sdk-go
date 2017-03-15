package main

import (
	"context"

	"encoding/json"

	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
	"github.com/Sirupsen/logrus"
)

type (
	sampleActivity struct{}
	sampleWorkflow struct {
		helper *samples.SampleHelper
	}

	sampleWorkflowParam struct {
		TotalBranches int
	}

	sampleActivityParam struct {
		CurrentBranchID int
		TotalBranches   int
	}
)

/**
* Activities need to implement interface cadence.Activity.
 */
func (a sampleActivity) Execute(context context.Context, input []byte) ([]byte, error) {
	param := &sampleActivityParam{}
	json.Unmarshal(input, param)

	logrus.Printf("Done %d of %d.", param.CurrentBranchID, param.TotalBranches)
	return nil, nil
}

func (a sampleActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: sampleActivityName}
}

/**
* Workflow need to implement interface cadence.Workflow
 */
func (w sampleWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	param := &sampleWorkflowParam{}
	err = json.Unmarshal(input, param)
	if err != nil {
		logrus.Panicf("Failed to unmarshal parameters.")
		return nil, err
	}

	var futures []cadence.Future
	// starts activities in parallel
	for i := 1; i <= param.TotalBranches; i++ {
		activityInput, _ := json.Marshal(&sampleActivityParam{i, param.TotalBranches})
		future := cadence.ExecuteActivityAsync(ctx, samples.ActivityParameters(sampleActivityTaskList, sampleActivityName, activityInput))
		futures = append(futures, future)
	}

	// wait until all futures are done
	for _, future := range futures {
		future.Get(ctx)
	}

	return nil, nil
}
