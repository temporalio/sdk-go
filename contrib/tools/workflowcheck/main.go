package main

import (
	"go.temporal.io/sdk/contrib/tools/workflowcheck/workflow"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(workflow.NewChecker(workflow.Config{}).NewAnalyzer())
}
