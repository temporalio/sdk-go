package pickfirst

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"code.uber.internal/devexp/cadence-client-go.git/client/cadence"
	"code.uber.internal/devexp/cadence-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

/**
 * This sample workflow execute activities in parallel branches, pick the result of the branch that completes first,
 * and then cancels other activities that are not finished yet.
 */

const (
	sampleWorkflowName = "sample_pick_first_workflow"
	sampleTaskList     = "sample_pick_first_tasklist"
	sampleActivityName = "sample_pick_first_activity"
)

type (
	sampleActivity struct{}
	sampleWorkflow struct{}

	sampleActivityParam struct {
		CurrentBranchID int
		TotalBranches   int
	}
)

// WorkflowConfig specify configuration for sample workflow
var WorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: sampleWorkflowName,
	TaskList:     sampleTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return sampleWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&sampleActivity{}},
	WorkflowInput: nil,
}

func (w sampleWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	pendingFutures := make(map[cadence.Future]cadence.CancelFunc)
	selector := cadence.NewSelector(ctx)
	var firstResponse string
	// starts multiple activities in parallel
	for i := 1; i <= 2; i++ {
		childCtx, cancelHandler := cadence.WithCancel(ctx)
		activityInput, _ := json.Marshal(&sampleActivityParam{i, 2})
		activityParams := common.ActivityParameters(sampleTaskList, sampleActivityName, activityInput)
		// Set WaitForCancellation to true to demonstrate the cancellation to the other activities. In real world case,
		// you might not care about them and could set WaitForCancellation to false (which is default value).
		activityParams.WaitForCancellation = true
		future := cadence.ExecuteActivityAsync(childCtx, activityParams)
		pendingFutures[future] = cancelHandler
		selector.AddFuture(future, func(v interface{}, err error) {
			// future is done, remove it from pending futures
			delete(pendingFutures, future)
			firstResponse = string(v.([]byte))
		})
	}
	// wait for any of the future to complete
	selector.Select(ctx)

	// now at least one future is complete, so cancel all other pending futures.
	for _, cancelHandler := range pendingFutures {
		cancelHandler()
	}

	// Wait for the pending activities to finish. This is just for demo purpose. In real world case, you don't have to
	// wait and can proceed. In that case, you want to set the WaitForCancellation to false (which is default value).
	for f := range pendingFutures {
		f.Get(ctx)
	}

	logrus.Infof("Workflow completed. Result: %s", firstResponse)

	return nil, nil
}

func (a sampleActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	param := &sampleActivityParam{}
	json.Unmarshal(input, param)

	// make the first branch run fast (finish in 2s), and others slow (finish in 10s)
	totalDuration := time.Second * 2
	if param.CurrentBranchID != 1 {
		totalDuration = time.Second * 10
	}
	elapsedDuration := time.Nanosecond
	for elapsedDuration < totalDuration {
		time.Sleep(time.Second)
		elapsedDuration += time.Second

		// record heartbeat every second to check if we are been cancelled
		heartbeatErr := cadence.RecordActivityHeartbeat(ctx, nil)
		if heartbeatErr == nil {
			// nothing goes wrong, continue our work
			logrus.Infof("Branch %d continue working...", param.CurrentBranchID)
		} else {
			// handle error cases, we are particularly interested the cancelled case for this sample.
			switch heartbeatErr.(type) {
			case cadence.CanceledError:
				// we have been cancelled
				msg := fmt.Sprintf("Branch %d is cancelled.", param.CurrentBranchID)
				logrus.Info(msg)
				return []byte(msg), cadence.NewCanceledError()
			default:
				logrus.Errorf("Branch %d see heartBeat failed with error: %v", param.CurrentBranchID, heartbeatErr)
				return []byte(""), heartbeatErr
			}
		}
	}

	msg := fmt.Sprintf("Branch %d done in %s.", param.CurrentBranchID, totalDuration)
	logrus.Info(msg)
	return []byte(msg), nil
}

func (a sampleActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: sampleActivityName}
}
