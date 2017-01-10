package cadence

// All code in this file is private to the package.

import (
	"golang.org/x/net/context"

	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
)

type (

	// asyncActivityClient for requesting activity execution
	asyncActivityClient interface {
		ExecuteActivity(parameters ExecuteActivityParameters, callback resultHandler)
	}

	activityEnvironment struct {
		taskToken         []byte
		workflowExecution WorkflowExecution
		activityID        string
		activityType      ActivityType
		identity          string
		service           m.TChanWorkflowService
	}
)

const activityEnvContextKey = "activityEnv"

func getActivityEnv(ctx context.Context) *activityEnvironment {
	env := ctx.Value(activityEnvContextKey)
	if env == nil {
		panic("getActivityEnv: Not an activity context")
	}
	return env.(*activityEnvironment)
}
