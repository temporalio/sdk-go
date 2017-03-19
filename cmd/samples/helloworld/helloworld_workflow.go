package helloworld

import (
	"context"

	"code.uber.internal/devexp/cadence-client-go.git/client/cadence"
	"code.uber.internal/devexp/cadence-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

const (
	sampleWorkflowName = "hello_world_workflow"
	sampleTaskList     = "hello_world_tasklist"
	sampleActivityName = "hello_world_activity"
)

type (
	helloworldWorkflow struct{}
	helloworldActivity struct{}
)

// WorkflowConfig specify configuration for sample workflow
var WorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: sampleWorkflowName,
	TaskList:     sampleTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return helloworldWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&helloworldActivity{}},
	WorkflowInput: []byte("Cadence"),
}

/**
 * Implementation of the hello world workflow
 */
func (w helloworldWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	helloworldResult, err := cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, sampleActivityName, input))
	if err != nil {
		logrus.Panicf("Error: %s\n", err.Error())
		return nil, err
	}

	logrus.Info("Workflow completed with result: ", string(helloworldResult))

	return nil, nil
}

/**
 * Implementation of the hello world activities
 */
func (a helloworldActivity) Execute(context context.Context, input []byte) ([]byte, error) {
	name := "World"
	if input != nil && len(input) > 0 {
		name = string(input)
	}
	return []byte("Hello " + name + "!"), nil
}

func (a helloworldActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: sampleActivityName}
}
