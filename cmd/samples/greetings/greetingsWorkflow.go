package main

import (
	"context"
	"encoding/json"
	"fmt"

	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
	"github.com/Sirupsen/logrus"
)

const (
	getGreetingActivityName = "getGreetingActivity"
	getNameActivityName     = "getNameActivity"
	sayGreetingActivityName = "sayGreetingActivity"
)

type (
	// Workflow Deciders and Activities.
	greetingsWorkflow   struct{}
	getNameActivity     struct{}
	getGreetingActivity struct{}
	sayGreetingActivity struct{}

	sayGreetingActivityRequest struct {
		Name     string
		Greeting string
	}
)

// Greetings Workflow Decider.
func (w greetingsWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	// Get Greeting.
	greetResult, err := cadence.ExecuteActivity(ctx, samples.ActivityParameters(sampleActivityTaskList, getGreetingActivityName, nil))
	if err != nil {
		logrus.Panicf("Marshalling failed with error: %+v", err)
		return nil, err
	}

	// Get Name.
	nameResult, err := cadence.ExecuteActivity(ctx, samples.ActivityParameters(sampleActivityTaskList, getNameActivityName, nil))
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
	workflowResult, err := cadence.ExecuteActivity(ctx, samples.ActivityParameters(sampleActivityTaskList, sayGreetingActivityName, sayGreetInput))
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
