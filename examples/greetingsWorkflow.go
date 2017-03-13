package examples

import (
	"context"
	"encoding/json"
	"fmt"

	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
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
	greetResult, err := cadence.ExecuteActivity(ctx, activityInfo("getGreetingActivity"))
	if err != nil {
		return nil, err
	}

	// Get Name.
	nameResult, err := cadence.ExecuteActivity(ctx, activityInfo("getNameActivity"))
	if err != nil {
		return nil, err
	}

	// Say Greeting.
	request := &sayGreetingActivityRequest{Name: string(nameResult), Greeting: string(greetResult)}
	_, err = cadence.ExecuteActivity(ctx, activityInfoWithInput("sayGreetingActivity", request))
	if err != nil {
		return nil, err
	}

	return nil, nil
}

// Get Name Activity.
func (g getNameActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("World"), nil
}

func (g getNameActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: "getNameActivity"}
}

// Get Greeting Activity.
func (ga getGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	return []byte("Hello"), nil
}

func (ga getGreetingActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: "getGreetingActivity"}
}

// Say Greeting Activity.
func (ga sayGreetingActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	greeetingParams := &sayGreetingActivityRequest{}
	err := json.Unmarshal(input, greeetingParams)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Saying Final Greeting: ")
	fmt.Printf("%s %s!\n", greeetingParams.Greeting, greeetingParams.Name)
	return nil, nil
}

func (ga sayGreetingActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: "sayGreetingActivity"}
}
