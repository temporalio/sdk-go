package worker

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

type Worker interface {
	Registry
}

type Registry interface {
	WorkflowRegistry
	ActivityRegistry
}

type WorkflowRegistry interface {
	RegisterWorkflow(w interface{})
	RegisterWorkflowWithOptions(w interface{}, options workflow.RegisterOptions)
}

type ActivityRegistry interface {
	RegisterActivity(a interface{})
	RegisterActivityWithOptions(a interface{}, options activity.RegisterOptions)
}
