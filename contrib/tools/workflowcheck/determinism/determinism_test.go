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
	identRefs["a.IgnoredCall"] = false
	identRefs["os.Stderr"] = false
	results := analysistest.Run(
		t,
		analysistest.TestData(),
		determinism.NewChecker(determinism.Config{
			IdentRefs: identRefs,
			SkipFiles: []*regexp.Regexp{
				regexp.MustCompile(`.*/should_skip\.go`),
			},
			Debug: true,
		}).NewAnalyzer(),
		"a",
	)
	if testing.Verbose() {
		// Dump the tree of the "a" package
		for _, result := range results {
			if result.Pass.Pkg.Name() == "a" {
				if res, _ := result.Result.(*determinism.Result); res != nil {
					for _, line := range res.Dump(false) {
						t.Log(line)
					}
				}
			}
		}
	}
}
