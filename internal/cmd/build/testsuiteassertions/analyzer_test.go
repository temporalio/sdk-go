package testsuiteassertions_test

import (
	"testing"

	"go.temporal.io/sdk/internal/cmd/build/testsuiteassertions"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), testsuiteassertions.Analyzer, "a")
}
