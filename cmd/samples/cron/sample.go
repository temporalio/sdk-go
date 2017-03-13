package main

import (
	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
)

const (
	sampleWorkflowName     = "sampleWorkflow"
	sampleActivityName     = "sampleActivity"
	sampleWorkflowTaskList = "sampleWorkflowTaskList"
	sampleActivityTaskList = "sampleActivityTaskList"
)

func main() {
	var h samples.SampleHelper
	h.SetupConfig()
	workflowFactory := func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return sampleWorkflow{}, nil
	}
	h.StartWorkflowWorker(sampleWorkflowTaskList, 1, workflowFactory)
	h.StartActivityWorker(sampleActivityTaskList, 1, []cadence.Activity{&sampleActivity{}})
	h.StartWorkflow(sampleWorkflowName, sampleWorkflowTaskList, nil, 60)

	select {}
}
