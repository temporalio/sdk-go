package determinism_test

import (
	"go/types"
	"regexp"
	"testing"

	"go.temporal.io/sdk/contrib/tools/workflowcheck/determinism"
	"golang.org/x/tools/go/analysis"
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

func TestEmptyPassFilesDoesNotExportPackageFact(t *testing.T) {
	exported := false
	pass := &analysis.Pass{
		Pkg: types.NewPackage("unsafe", "unsafe"),
		ExportPackageFact: func(analysis.Fact) {
			exported = true
		},
	}
	p, err := determinism.NewChecker(determinism.Config{}).Run(pass)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(p) != 0 {
		t.Fatalf("expected empty non-determinisms, got %v", p)
	}
	if exported {
		t.Fatal("expected ExportPackageFact not to be called for pass without files")
	}
}
