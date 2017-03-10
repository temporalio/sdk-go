package main

import (
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
)

const (
	sampleWorkflowName     = "greetingsWorkflow"
	sampleWorkflowTaskList = "greetingsWorkflowTaskList"
	sampleActivityTaskList = "greetingsActivityTaskList"
)

func main() {
	var h samples.SampleHelper
	h.SetupConfig()
	workflowFactory := func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return greetingsWorkflow{}, nil
	}
	h.StartWorkflowWorker(sampleWorkflowTaskList, 1, workflowFactory)
	activities := []cadence.Activity{&getNameActivity{}, &getGreetingActivity{}, &sayGreetingActivity{}}
	h.StartActivityWorker(sampleActivityTaskList, 1, activities)
	h.StartWorkflow(sampleWorkflowName, sampleWorkflowTaskList, []byte("Cadence"), 60)

	select {}
}
