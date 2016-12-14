package examples

import (
	"encoding/json"
	"fmt"

	"code.uber.internal/devexp/minions-client-go.git/client/flow"
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
func (w greetingsWorkflow) Execute(ctx flow.Context, input []byte) (result []byte, err flow.Error) {
	// Get Greeting.
	greetResult, err := ctx.ExecuteActivity(activityInfo("getGreetingActivity"))
	if err != nil {
		return nil, err
	}

	// Get Name.
	nameResult, err := ctx.ExecuteActivity(activityInfo("getNameActivity"))
	if err != nil {
		return nil, err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: string(nameResult), Greeting: string(greetResult)}
	_, err = ctx.ExecuteActivity(activityInfoWithInput("sayGreetingActivity", request))
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// Get Name Activity.
func (g getNameActivity) Execute(context flow.ActivityExecutionContext, input []byte) ([]byte, flow.Error) {
	return []byte("World"), nil
}

// Get Greeting Activity.
func (ga getGreetingActivity) Execute(context flow.ActivityExecutionContext, input []byte) ([]byte, flow.Error) {
	return []byte("Hello"), nil
}

// Say Greeting Activity.
func (ga sayGreetingActivity) Execute(context flow.ActivityExecutionContext, input []byte) ([]byte, flow.Error) {
	greeetingParams := &sayGreetingActivityRequest{}
	err := json.Unmarshal(input, greeetingParams)
	if err != nil {
		return nil, flow.NewError(err.Error(), nil)
	}

	fmt.Printf("Saying Final Greeting: ")
	fmt.Printf("%s %s!\n", greeetingParams.Greeting, greeetingParams.Name)
	return nil, nil
}
