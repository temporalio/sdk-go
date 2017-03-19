package branch

import (
	"context"
	"encoding/json"

	"code.uber.internal/devexp/cadence-client-go.git/client/cadence"
	"code.uber.internal/devexp/cadence-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

const (
	sampleWorkflowName = "sample_branch_workflow"
	sampleTaskList     = "sample_branch_tasklist"
	sampleActivityName = "sample_branch_activity"
)

type (
	sampleActivity struct{}
	sampleWorkflow struct{}

	sampleWorkflowParam struct {
		TotalBranches int
	}

	sampleActivityParam struct {
		CurrentBranchID int
		TotalBranches   int
	}
)

var _input, _ = json.Marshal(&sampleWorkflowParam{3})

// WorkflowConfig specify configuration for sample workflow
var WorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: sampleWorkflowName,
	TaskList:     sampleTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return sampleWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&sampleActivity{}},
	WorkflowInput: _input,
}

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
		future := cadence.ExecuteActivityAsync(ctx, common.ActivityParameters(sampleTaskList, sampleActivityName, activityInput))
		futures = append(futures, future)
	}

	// wait until all futures are done
	for _, future := range futures {
		future.Get(ctx)
	}

	logrus.Info("Workflow completed.")

	return nil, nil
}
