// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
			IdentRefs:             identRefs,
			SkipFiles:             []*regexp.Regexp{regexp.MustCompile(`.*/should_skip\.go`)},
			Debug:                 false, // Set to true to see details
			DebugfFunc:            t.Logf,
			EnableObjectFacts:     true,
			DeterministicWrappers: map[string][]string{"a": {"DeterministicWrapper"}},
		}).NewAnalyzer(),
		"a",
	)
}
