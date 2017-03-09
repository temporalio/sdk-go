package main

// This is the "Hello World" sample code for cadence workflow.
// The logic of the workflow and its activities are defined in helloworldWorkflow.go
//

import (
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
)

const (
	helloworldWorkflowName     = "helloworldWorkflow"
	helloworldActivityName     = "helloworldActivity"
	helloworldWorkflowTaskList = "helloworldWorkflowTaskList"
	helloworldActivityTaskList = "helloworldActivityTaskList"
)

func main() {
	var h samples.SampleHelper
	h.SetupConfig()
	workflowFactory := func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return helloworldWorkflow{&h}, nil
	}

	h.StartWorkflowWorker(helloworldWorkflowTaskList, 1, workflowFactory)
	h.StartActivityWorker(helloworldActivityTaskList, 1, []cadence.Activity{&helloworldActivity{}})
	h.StartWorkflow(helloworldWorkflowName, helloworldWorkflowTaskList, []byte("Cadence"), 60)

	<-h.ResultCh // wait for result
}
