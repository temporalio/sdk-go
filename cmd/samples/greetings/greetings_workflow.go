package greetings

import (
	"context"
	"encoding/json"
	"fmt"

	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

const (
	sampleWorkflowName      = "sample_greetings_workflow"
	sampleTaskList          = "sample_greetings_tasklist"
	getGreetingActivityName = "sample_getGreetingActivity"
	getNameActivityName     = "sample_getNameActivity"
	sayGreetingActivityName = "sample_sayGreetingActivity"
)

type (
	// Workflow Deciders and Activities.
	sampleWorkflow      struct{}
	getNameActivity     struct{}
	getGreetingActivity struct{}
	sayGreetingActivity struct{}

	sayGreetingActivityRequest struct {
		Name     string
		Greeting string
	}
)

// WorkflowConfig specify configuration for sample workflow
var WorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: sampleWorkflowName,
	TaskList:     sampleTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return sampleWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&getNameActivity{}, &getGreetingActivity{}, &sayGreetingActivity{}},
	WorkflowInput: nil,
}

// Greetings Workflow Decider.
func (w sampleWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	// Get Greeting.
	greetResult, err := cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, getGreetingActivityName, nil))
	if err != nil {
		logrus.Panicf("Marshalling failed with error: %+v", err)
		return nil, err
	}

	// Get Name.
	nameResult, err := cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, getNameActivityName, nil))
	if err != nil {
		logrus.Panicf("Marshalling failed with error: %+v", err)
		return nil, err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: string(nameResult), Greeting: string(greetResult)}
	sayGreetInput, err := json.Marshal(request)
	if err != nil {
		logrus.Panicf("Marshalling failed with error: %+v", err)
		return nil, err
	}
	workflowResult, err := cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, sayGreetingActivityName, sayGreetInput))
	if err != nil {
		logrus.Panicf("Marshalling failed with error: %+v", err)
		return nil, err
	}

	logrus.Info("Workflow completed with result: ", string(workflowResult))

	return nil, nil
}

// Get Name Activity.
func (g getNameActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

func (g getNameActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: getNameActivityName}
}

// Get Greeting Activity.
func (ga getGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("Hello"), nil
}

func (ga getGreetingActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: getGreetingActivityName}
}

// Say Greeting Activity.
func (ga sayGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	greetingRequest := &sayGreetingActivityRequest{}
	err := json.Unmarshal(input, greetingRequest)
	if err != nil {
		return nil, err
	}

	result := fmt.Sprintf("Greeting: %s %s!\n", greetingRequest.Greeting, greetingRequest.Name)

	return []byte(result), nil
}

func (ga sayGreetingActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: sayGreetingActivityName}
}
