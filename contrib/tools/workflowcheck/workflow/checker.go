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

package workflow

import (
	"fmt"
	"go/ast"
	"go/types"
	"log"
	"os"
	"regexp"
	"strings"

	"go.temporal.io/sdk/contrib/tools/workflowcheck/determinism"
	"golang.org/x/tools/go/analysis"
	"gopkg.in/yaml.v2"
)

// DefaultIdentRefs are additional overrides of determinism.DefaultIdentRefs for
// safe Temporal library functions.
var DefaultIdentRefs = determinism.DefaultIdentRefs.Clone().SetAll(determinism.IdentRefs{
	// Reported as non-deterministic because it internally starts a goroutine, so
	// mark deterministic explicitly
	"go.temporal.io/sdk/internal.propagateCancel": false,
	// Reported as non-deterministic because it iterates over a map, so mark
	// deterministic explicitly
	"(*go.temporal.io/sdk/internal.cancelCtx).cancel": false,
	// Reported as non-deterministic because it iterates over a map, just takes
	// the size of the map, so mark deterministic explicitly
	"(go.temporal.io/sdk/internal.SearchAttributes).Size": false,
	// Reported as non-deterministic because it iterates over a map, result is sorted
	// so mark deterministic explicitly
	"go.temporal.io/sdk/internal.DeterministicKeys": false,
	// Reported as non-deterministic because it iterates over a map, result is sorted
	// so mark deterministic explicitly
	"go.temporal.io/sdk/internal.DeterministicKeysFunc": false,
})

// Config is config for NewChecker.
type Config struct {
	// If empty, uses DefaultIdentRefs.
	IdentRefs determinism.IdentRefs
	// If nil, uses log.Printf.
	DebugfFunc func(string, ...interface{})
	// Must be set to true to see advanced debug logs.
	Debug bool
	// Must be set to true to see advanced determinism debug logs.
	DeterminismDebug bool
	// If set, the file and line/col position is present on nested errors.
	IncludePosOnMessage bool
	// If set, the determinism checker will include facts per object
	EnableObjectFacts bool
	// If set, the output uses "->" instead of "\n" as the hierarchy separator.
	SingleLine bool
}

// Checker checks if functions passed RegisterWorkflow are non-deterministic
// based on the results from the checker of the adjacent determinism package.
type Checker struct {
	DebugfFunc          func(string, ...interface{})
	Debug               bool
	IncludePosOnMessage bool
	Determinism         *determinism.Checker
	SingleLine          bool
}

// NewChecker creates a Checker for the given config.
func NewChecker(config Config) *Checker {
	// Set default refs but we don't have to clone since the determinism
	// constructor will do that
	if config.IdentRefs == nil {
		config.IdentRefs = DefaultIdentRefs
	}
	// Default debug
	if config.DebugfFunc == nil {
		config.DebugfFunc = log.Printf
	}
	// Build checker
	return &Checker{
		DebugfFunc:          config.DebugfFunc,
		Debug:               config.Debug,
		IncludePosOnMessage: config.IncludePosOnMessage,
		Determinism: determinism.NewChecker(determinism.Config{
			IdentRefs:                         config.IdentRefs,
			DebugfFunc:                        config.DebugfFunc,
			Debug:                             config.DeterminismDebug,
			EnableObjectFacts:                 config.EnableObjectFacts,
			AcceptsNonDeterministicParameters: map[string][]string{"go.temporal.io/sdk/workflow": {"SideEffect", "MutableSideEffect"}},
		}),
	}
}

func (c *Checker) debugf(f string, v ...interface{}) {
	if c.Debug {
		c.DebugfFunc(f, v...)
	}
}

// NewAnalyzer creates a Go analysis analyzer that can be used in existing
// tools. There is a -config flag for setting configuration, a -workflow-debug
// flag for enabling debug logs, a -determinism-debug flag for enabling
// determinism debug logs, and a -show-pos flag for showing position on nested
// errors. This analyzer does not have any results but does set the same
// facts as the determinism analyzer (*determinism.NonDeterminisms).
func (c *Checker) NewAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:      "workflowcheck",
		Doc:       "Analyzes all RegisterWorkflow functions for non-determinism",
		Run:       func(p *analysis.Pass) (interface{}, error) { return nil, c.Run(p) },
		FactTypes: []analysis.Fact{&determinism.PackageNonDeterminisms{}, &determinism.NonDeterminisms{}},
	}
	// Set flags
	a.Flags.Var(configFileFlag{c.Determinism}, "config", "configuration file")
	a.Flags.BoolVar(&c.Debug, "workflow-debug", c.Debug, "show workflow debug output")
	a.Flags.BoolVar(&c.Determinism.Debug, "determinism-debug", c.Determinism.Debug, "show determinism debug output")
	a.Flags.BoolVar(&c.IncludePosOnMessage, "show-pos", c.IncludePosOnMessage,
		"show file positions on determinism messages")
	a.Flags.BoolVar(&c.SingleLine, "single-line", c.SingleLine,
		"use '->' instead of newline between hierarchies of non-determinism")
	return a
}

// Run executes this checker for the given pass.
func (c *Checker) Run(pass *analysis.Pass) error {
	hierarchySeparator, depthRepeat := "\n", "  "
	if c.SingleLine {
		hierarchySeparator, depthRepeat = " -> ", ""
	}

	// If it's the workflow package, we assume the entire package is deterministic
	// so we don't run a pass on it
	if pass.Pkg.Path() == "go.temporal.io/sdk/workflow" {
		return nil
	}

	// Run determinism pass
	if _, err := c.Determinism.Run(pass); err != nil {
		return err
	}
	c.debugf("$ Checking package %v", pass.Pkg.Path())
	lookupCache := determinism.NewPackageLookupCache(pass)
	// Check every register workflow invocation
	for _, file := range pass.Files {
		// Get ignore map for this file
		ignoreMap := map[ast.Node]struct{}{}
		determinism.UpdateIgnoreMap(pass.Fset, file, ignoreMap)

		ast.Inspect(file, func(n ast.Node) bool {
			// Only handle calls with followable function pointers
			_, isIgnored := ignoreMap[n]
			for k := range ignoreMap {
				asExprStmt, _ := k.(*ast.ExprStmt)
				if asExprStmt != nil && asExprStmt.X == n {
					isIgnored = true
				}
			}
			funcDecl, _ := n.(*ast.FuncDecl)
			if funcDecl == nil {
				return true
			}
			c.debugf("Checking node %v", funcDecl.Name.Name)
			if isIgnored || !isWorkflowFunc(funcDecl, pass) {
				return true
			}
			fn, _ := pass.TypesInfo.ObjectOf(funcDecl.Name).(*types.Func)
			c.debugf("Checking workflow function %v", fn.FullName())
			// Get non-determinisms of that package and check
			packageNonDeterminisms := lookupCache.PackageNonDeterminisms(fn.Pkg())
			if nonDeterminisms := packageNonDeterminisms[fn.FullName()]; len(nonDeterminisms) > 0 {
				// One report per reason
				for _, reason := range nonDeterminisms {
					lines := determinism.NonDeterminisms{reason}.AppendChildReasonLines(
						fn.FullName(), nil, 0, depthRepeat, c.IncludePosOnMessage, fn.Pkg(), lookupCache, map[string]bool{})
					pass.Report(analysis.Diagnostic{Pos: fn.Pos(), Message: strings.Join(lines, hierarchySeparator)})
				}
			}
			return true
		})
	}
	return nil
}

// isWorkflowFunc checks if f has workflow.Context as a first parameter.
func isWorkflowFunc(f *ast.FuncDecl, pass *analysis.Pass) bool {
	if f.Type.Params == nil || len(f.Type.Params.List) == 0 {
		return false
	}
	firstParam := f.Type.Params.List[0]
	typeInfo := pass.TypesInfo.TypeOf(firstParam.Type)
	named, _ := typeInfo.(*types.Named)
	if named == nil {
		return false
	}
	obj := named.Obj()
	if obj.Pkg() == nil || obj.Name() != "Context" {
		return false
	}
	path := obj.Pkg().Path()
	return path == "go.temporal.io/sdk/workflow" || path == "go.temporal.io/sdk/internal"
}

type configFileFlag struct{ checker *determinism.Checker }

func (configFileFlag) String() string { return "<built-in>" }

func (c configFileFlag) Set(flag string) error {
	// Load the file into YAML
	b, err := os.ReadFile(flag)
	if err != nil {
		return fmt.Errorf("failed reading config: %w", err)
	}

	config := struct {
		Decls determinism.IdentRefs
		Skip  []string
	}{}
	if err := yaml.Unmarshal(b, &config); err != nil {
		return fmt.Errorf("failed parsing config file: %w", err)
	}

	// Apply all the ident refs and skip regexes
	c.checker.IdentRefs.SetAll(config.Decls)
	for _, skip := range config.Skip {
		r, err := regexp.Compile(skip)
		if err != nil {
			return fmt.Errorf("invalid skip regex %v: %w", skip, err)
		}
		c.checker.SkipFiles = append(c.checker.SkipFiles, r)
	}
	return nil
}
