package determinism_test

import (
	"regexp"
	"testing"

	"go.temporal.io/sdk/contrib/tools/workflowcheck/determinism"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	identRefs := determinism.DefaultIdentRefs.Clone()
	identRefs["a.BadCall"] = true
	identRefs["a.BadVar"] = true
	identRefs["(a.SomeInterface).BadCall"] = true
	identRefs["a.IgnoredCall"] = false
	identRefs["os.Stderr"] = false
	analysistest.Run(
		t,
		analysistest.TestData(),
		determinism.NewChecker(determinism.Config{
			IdentRefs:                         identRefs,
			SkipFiles:                         []*regexp.Regexp{regexp.MustCompile(`.*/should_skip\.go`)},
			Debug:                             false, // Set to true to see details
			DebugfFunc:                        t.Logf,
			EnableObjectFacts:                 true,
			AcceptsNonDeterministicParameters: map[string][]string{"a": {"DeterministicWrapper"}},
		}).NewAnalyzer(),
		"a",
	)
}
