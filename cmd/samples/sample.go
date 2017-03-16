package main

import (
	"flag"
	"math/rand"
	"time"

	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/branch"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/choice"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/common"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/cron"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/greetings"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/helloworld"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples/pickfirst"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	workflowConfig := getSampleWorkflowConfig()

	var h common.SampleHelper
	h.SetupServiceConfig()
	h.StartWorkflowWorker(workflowConfig.TaskList, 1, workflowConfig.WorkflowFactory)
	h.StartActivityWorker(workflowConfig.TaskList, 2, workflowConfig.Activities)
	h.StartWorkflow(workflowConfig.WorkflowName, workflowConfig.TaskList, workflowConfig.WorkflowInput)

	// We don't have visibility API yet, so there is no easy way for client to know when the submitted workflow is done.
	// The workers (WorkflowWorker and ActivityWorker) are supposed to be long running process that should not exit.
	// Use select{} to block indefinitely for samples, you can quit by CMD+C when you see the result print to console.
	// Please note that when the Execute method of workflow returns, the workers are still reporting the workflow
	// completed to cadence server. So we cannot quit the program immediately when Execute finishes.
	select {}
}

func getSampleWorkflowConfig() common.SampleWorkflowConfig {
	var sampleCase string
	flag.StringVar(&sampleCase, "c", "", "Sample case to run.")
	flag.Parse()

	switch sampleCase {
	case "helloworld":
		return helloworld.WorkflowConfig
	case "greetings":
		return greetings.WorkflowConfig
	case "cron":
		return cron.WorkflowConfig
	case "branch":
		return branch.WorkflowConfig
	case "pickfirst":
		return pickfirst.WorkflowConfig
	case "choice":
		return choice.WorkflowConfig
	case "multichoice":
		return choice.MultiChoiceWorkflowConfig
	default:
		return helloworld.WorkflowConfig
	}
}
