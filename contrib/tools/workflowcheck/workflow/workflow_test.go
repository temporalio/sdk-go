package workflow_test

import (
	"testing"

	"go.temporal.io/sdk/contrib/tools/workflowcheck/workflow"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	// This intentionally only does a few tests. Most of the tests for
	// non-determinism are in ../determinism/determinism_test.go.
	analysistest.Run(
		t,
		analysistest.TestData(),
		workflow.NewChecker(workflow.Config{}).NewAnalyzer(),
		"a",
	)
}
