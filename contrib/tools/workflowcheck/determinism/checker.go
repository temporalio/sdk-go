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

package determinism

import (
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// Config is config for NewChecker.
type Config struct {
	// If empty, uses DefaultIdentRefs.
	IdentRefs IdentRefs
	// If file matches any here, it is not checked at all.
	SkipFiles []*regexp.Regexp
	// If nil, uses log.Printf.
	DebugfFunc func(string, ...interface{})
	// Must be set to true to see advanced debug logs.
	Debug bool
	// Whether to export a *NonDeterminisms fact per object.
	EnableObjectFacts bool
}

// Checker is a checker that can run analysis passes to check for
// non-deterministic code.
type Checker struct{ Config }

// NewChecker creates a Checker for the given config.
func NewChecker(config Config) *Checker {
	// Set default refs and clone
	if config.IdentRefs == nil {
		config.IdentRefs = DefaultIdentRefs
	}
	config.IdentRefs = config.IdentRefs.Clone()
	// Default debug
	if config.DebugfFunc == nil {
		config.DebugfFunc = log.Printf
	}
	// Build checker
	return &Checker{config}
}

// NewAnalyzer creates a Go analysis analyzer that can be used in existing
// tools. There is a -set-decl flag for adding ident refs overrides and a
// -determinism-debug flag for enabling debug logs. The result is Result and the
// facts on functions are *NonDeterminisms.
func (c *Checker) NewAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:       "determinism",
		Doc:        "Analyzes all functions and marks whether they are deterministic",
		Run:        func(p *analysis.Pass) (interface{}, error) { return c.Run(p) },
		ResultType: reflect.TypeOf(PackageNonDeterminisms{}),
		FactTypes:  []analysis.Fact{&PackageNonDeterminisms{}, &NonDeterminisms{}},
	}
	// Set flags
	a.Flags.Var(NewIdentRefsFlag(c.IdentRefs), "set-decl",
		"qualified function/var to include/exclude, overriding the default (append '=false' to exclude)")
	a.Flags.BoolVar(&c.Debug, "determinism-debug", c.Debug, "show debug output")
	return a
}

func (c *Checker) debugf(f string, v ...interface{}) {
	if c.Debug {
		c.DebugfFunc(f, v...)
	}
}

// Run executes this checker for the given pass and stores the fact.
func (c *Checker) Run(pass *analysis.Pass) (PackageNonDeterminisms, error) {
	c.debugf("Checking package %v", pass.Pkg.Path())

	// Collect all top-level func decls and their types. Also mark var decls as
	// non-deterministic if pattern matches.
	funcDecls := map[*types.Func]*ast.FuncDecl{}
	coll := &collector{
		checker:     c,
		pass:        pass,
		lookupCache: NewPackageLookupCache(pass),
		nonDetVars:  map[*types.Var]NonDeterminisms{},
		ignoreMap:   map[ast.Node]struct{}{},
		funcInfos:   map[*types.Func]*funcInfo{},
	}
	for _, file := range pass.Files {
		// Skip this file if it matches any regex
		fileName := filepath.ToSlash(pass.Fset.File(file.Package).Name())
		skipFile := false
		for _, skipPattern := range c.SkipFiles {
			if skipFile = skipPattern.MatchString(fileName); skipFile {
				break
			}
		}
		if skipFile {
			continue
		}

		// Update ignore map
		updateIgnoreMap(pass.Fset, file, coll.ignoreMap)

		// Collect the decls to check and check vars/iface patterns
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				// Collect top-level func
				if funcType, _ := pass.TypesInfo.ObjectOf(decl.Name).(*types.Func); funcType != nil {
					funcDecls[funcType] = decl
				}
			case *ast.GenDecl:
				// Set top-level vars that match pattern as non-deterministic
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					// See if the top-level vars match patterns
					case *ast.ValueSpec:
						for _, varName := range spec.Names {
							if varType, _ := pass.TypesInfo.ObjectOf(varName).(*types.Var); varType != nil && varType.Pkg() != nil {
								fullName := varType.Pkg().Path() + "." + varType.Name()
								if c.IdentRefs[fullName] {
									c.debugf("Marking %v as non-deterministic because it matched a pattern", fullName)
									pos := pass.Fset.Position(varType.Pos())
									coll.nonDetVars[varType] = NonDeterminisms{&ReasonDecl{SourcePos: &pos}}
								}
							}
						}
					// See if any interface funcs match patterns
					case *ast.TypeSpec:
						if iface, _ := pass.TypesInfo.TypeOf(spec.Type).(*types.Interface); iface != nil {
							// Only need to match explicitly defined methods
							for i := 0; i < iface.NumExplicitMethods(); i++ {
								info := coll.funcInfo(iface.ExplicitMethod(i))
								if c.IdentRefs[info.fn.FullName()] {
									c.debugf("Marking %v as non-deterministic because it matched a pattern", info.fn.FullName())
									pos := pass.Fset.Position(spec.Pos())
									info.reasons = append(info.reasons, &ReasonDecl{SourcePos: &pos})
								}
							}
						}
					}
				}
			}
		}
	}

	// Build collector and do initial pass for each function async

	// Parallelize to the number of CPUs
	maxAtOnce := runtime.NumCPU()
	if maxAtOnce < 1 {
		maxAtOnce = 1
	}
	doneCh := make(chan struct{}, maxAtOnce)
	var running int
	for fn, decl := range funcDecls {
		// If we've filled the channel, wait
		if running == cap(doneCh) {
			<-doneCh
		} else {
			running++
		}
		go func(fn *types.Func, decl *ast.FuncDecl) {
			coll.collectFuncInfo(fn, decl)
			doneCh <- struct{}{}
		}(fn, decl)
	}
	// Wait for the rest to finish
	for i := 0; i < running; i++ {
		<-doneCh
	}
	// Build facts in second pass
	return coll.applyFacts(), nil
}

func updateIgnoreMap(fset *token.FileSet, f *ast.File, m map[ast.Node]struct{}) {
	// Collect only the ignore comments
	var comments []*ast.CommentGroup
	for _, group := range f.Comments {
		// Check each comment in list so Godoc and others can be in any order
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//workflowcheck:ignore") {
				comments = append(comments, group)
				break
			}
		}
	}
	// Bail if no comments
	if len(comments) == 0 {
		return
	}
	// Add all present in comment map to ignore map
	for k := range ast.NewCommentMap(fset, f, comments) {
		m[k] = struct{}{}
	}
}

type collector struct {
	// Concurrency-safe/immutable fields
	checker     *Checker
	pass        *analysis.Pass
	lookupCache *PackageLookupCache
	nonDetVars  map[*types.Var]NonDeterminisms
	ignoreMap   map[ast.Node]struct{}

	funcInfos     map[*types.Func]*funcInfo
	funcInfosLock sync.Mutex
}

type funcInfo struct {
	fn                   *types.Func
	reasons              NonDeterminisms
	samePackageCalls     map[*funcInfo]token.Pos
	samePackageCallsLock sync.Mutex
	factsApplied         bool
}

// Concurrency safe
func (f *funcInfo) addSamePackageCall(callee *funcInfo, pos token.Pos) {
	// Ignore direct recursive calls
	if f == callee {
		return
	}
	f.samePackageCallsLock.Lock()
	defer f.samePackageCallsLock.Unlock()
	// Only if not already there so we can capture the first token
	if _, ok := f.samePackageCalls[callee]; !ok {
		f.samePackageCalls[callee] = pos
	}
}

func (c *collector) funcInfo(fn *types.Func) *funcInfo {
	c.funcInfosLock.Lock()
	defer c.funcInfosLock.Unlock()
	info := c.funcInfos[fn]
	if info == nil {
		info = &funcInfo{fn: fn, samePackageCalls: map[*funcInfo]token.Pos{}}
		c.funcInfos[fn] = info
	}
	return info
}

func (c *collector) externalFuncNonDeterminisms(fn *types.Func) NonDeterminisms {
	return c.lookupCache.PackageNonDeterminisms(fn.Pkg())[fn.FullName()]
}

func (c *collector) externalVarNonDeterminisms(v *types.Var) NonDeterminisms {
	return c.lookupCache.PackageNonDeterminisms(v.Pkg())[v.Name()]
}

func (c *collector) collectFuncInfo(fn *types.Func, decl *ast.FuncDecl) {
	info := c.funcInfo(fn)

	// If matches a pattern, can eagerly stop here
	match, ok := c.checker.IdentRefs[fn.FullName()]
	if ok {
		if match {
			c.checker.debugf("Marking %v as non-deterministic because it matched a pattern", fn.FullName())
			pos := c.pass.Fset.Position(fn.Pos())
			info.reasons = append(info.reasons, &ReasonDecl{SourcePos: &pos})
		} else {
			c.checker.debugf("Skipping %v because it matched a pattern", fn.FullName())
		}
		return
	}

	// Walk
	ast.Inspect(decl, func(n ast.Node) bool {
		// Go no deeper if ignoring
		if _, ignored := c.ignoreMap[n]; ignored {
			return false
		}

		switch n := n.(type) {
		case *ast.CallExpr:
			// Get the callee
			if callee, _ := typeutil.Callee(c.pass.TypesInfo, n).(*types.Func); callee != nil {
				// If it's in a different package, check externals
				if c.pass.Pkg != callee.Pkg() {
					calleeReasons := c.externalFuncNonDeterminisms(callee)
					if len(calleeReasons) > 0 {
						c.checker.debugf("Marking %v as non-deterministic because it calls %v", fn.FullName(), callee.FullName())
						pos := c.pass.Fset.Position(n.Pos())
						info.reasons = append(info.reasons, &ReasonFuncCall{
							SourcePos: &pos,
							FuncName:  callee.FullName(),
						})
					}
				} else {
					// Otherwise, we simply add as a same-package call
					info.addSamePackageCall(c.funcInfo(callee), n.Pos())
				}
			}
		case *ast.GoStmt:
			// Any go statement is non-deterministic
			c.checker.debugf("Marking %v as non-deterministic because it starts a goroutine", fn.FullName())
			pos := c.pass.Fset.Position(n.Pos())
			info.reasons = append(info.reasons, &ReasonConcurrency{SourcePos: &pos, Kind: ConcurrencyKindGo})
		case *ast.Ident:
			// Check if ident is for a non-deterministic var
			if varType, _ := c.pass.TypesInfo.ObjectOf(n).(*types.Var); varType != nil {
				// If it's in a different package, check for external non-determinisms.
				// Otherwise check local.
				var nonDetVar NonDeterminisms
				if c.pass.Pkg != varType.Pkg() {
					nonDetVar = c.externalVarNonDeterminisms(varType)
				} else {
					nonDetVar = c.nonDetVars[varType]
				}
				if len(nonDetVar) > 0 {
					c.checker.debugf("Marking %v as non-deterministic because it accesses %v.%v",
						fn.FullName(), varType.Pkg().Path(), varType.Name())
					pos := c.pass.Fset.Position(n.Pos())
					info.reasons = append(info.reasons, &ReasonVarAccess{
						SourcePos: &pos,
						VarName:   varType.Pkg().Path() + "." + varType.Name(),
					})
				}
			}
		case *ast.RangeStmt:
			// Map and chan ranges are non-deterministic
			rangeType := c.pass.TypesInfo.TypeOf(n.X)
			// Unwrap named type
			for {
				if namedType, _ := rangeType.(*types.Named); namedType != nil {
					rangeType = namedType.Underlying()
				} else {
					break
				}
			}
			switch rangeType.(type) {
			case *types.Map:
				c.checker.debugf("Marking %v as non-deterministic because it iterates over a map", fn.FullName())
				pos := c.pass.Fset.Position(n.Pos())
				info.reasons = append(info.reasons, &ReasonMapRange{SourcePos: &pos})
			case *types.Chan:
				c.checker.debugf("Marking %v as non-deterministic because it iterates over a channel", fn.FullName())
				pos := c.pass.Fset.Position(n.Pos())
				info.reasons = append(info.reasons, &ReasonConcurrency{SourcePos: &pos, Kind: ConcurrencyKindRange})
			}
		case *ast.SendStmt:
			// Any send statement is non-deterministic
			c.checker.debugf("Marking %v as non-deterministic because it sends to a channel", fn.FullName())
			pos := c.pass.Fset.Position(n.Pos())
			info.reasons = append(info.reasons, &ReasonConcurrency{SourcePos: &pos, Kind: ConcurrencyKindSend})
		case *ast.UnaryExpr:
			// If the operator is a receive, it is non-deterministic
			if n.Op == token.ARROW {
				c.checker.debugf("Marking %v as non-deterministic because it receives from a channel", fn.FullName())
				pos := c.pass.Fset.Position(n.Pos())
				info.reasons = append(info.reasons, &ReasonConcurrency{SourcePos: &pos, Kind: ConcurrencyKindRecv})
			}
		}
		return true
	})
}

// Expects to be called as second pass after all func infos collected.
func (c *collector) applyFacts() PackageNonDeterminisms {
	p := PackageNonDeterminisms{}
	// Just run for each. Even though recursive, likely no benefit from
	// parallelizing.
	for _, info := range c.funcInfos {
		c.applyFuncNonDeterminisms(info, p)
		// Export fact if requested
		if c.checker.EnableObjectFacts && len(info.reasons) > 0 {
			c.pass.ExportObjectFact(info.fn, &info.reasons)
		}
	}

	// Add non-deterministic vars to the result set too
	for v := range c.nonDetVars {
		pos := c.pass.Fset.Position(v.Pos())
		det := NonDeterminisms{&ReasonDecl{SourcePos: &pos}}
		p[v.Name()] = det
		// Export fact if requested
		if c.checker.EnableObjectFacts {
			c.pass.ExportObjectFact(v, &det)
		}
	}

	// Export package fact
	c.pass.ExportPackageFact(&p)
	return p
}

func (c *collector) applyFuncNonDeterminisms(f *funcInfo, p PackageNonDeterminisms) {
	if f.factsApplied {
		return
	}
	f.factsApplied = true
	// Recursively call for same-package calls and then see if they have reasons
	// for non-determinism
	for child, pos := range f.samePackageCalls {
		c.applyFuncNonDeterminisms(child, p)
		if len(child.reasons) > 0 {
			c.checker.debugf("Marking %v as non-deterministic because it calls %v", f.fn.FullName(), child.fn.FullName())
			pos := c.pass.Fset.Position(pos)
			f.reasons = append(f.reasons, &ReasonFuncCall{
				SourcePos: &pos,
				FuncName:  child.fn.FullName(),
			})
		}
	}
	// If we have reasons, place on package non-det
	if len(f.reasons) > 0 {
		p[f.fn.FullName()] = f.reasons
	}
}

// PackageLookupCache caches fact lookups across packages.
type PackageLookupCache struct {
	pass                       *analysis.Pass
	packageNonDeterminisms     map[*types.Package]PackageNonDeterminisms
	packageNonDeterminismsLock sync.Mutex
}

// NewPackageLookupCache creates a PackageLookupCache.
func NewPackageLookupCache(pass *analysis.Pass) *PackageLookupCache {
	return &PackageLookupCache{pass: pass, packageNonDeterminisms: map[*types.Package]PackageNonDeterminisms{}}
}

// PackageNonDeterminisms returns non-determinisms for the package or an empty
// set if none found.
func (p *PackageLookupCache) PackageNonDeterminisms(pkg *types.Package) PackageNonDeterminisms {
	if pkg == nil {
		return nil
	}
	// The import has to be done under lock too since it's not concurrency safe
	p.packageNonDeterminismsLock.Lock()
	defer p.packageNonDeterminismsLock.Unlock()
	ret, exists := p.packageNonDeterminisms[pkg]
	if !exists {
		// We don't care whether it can be imported, we store in the map either way
		// to save future lookups
		p.pass.ImportPackageFact(pkg, &ret)
		p.packageNonDeterminisms[pkg] = ret
	}
	return ret
}

// PackageNonDeterminismsFromName returns the package for the given name and its
// non-determinisms via PackageNonDeterminisms. The package name must be
// directly imported from the given package in scope. Nil is returned for a
// package that is not found.
func (p *PackageLookupCache) PackageNonDeterminismsFromName(
	pkgInScope *types.Package,
	importedPkg string,
) (*types.Package, PackageNonDeterminisms) {
	// Package must be imported from the one in scope or be the one in scope
	var pkg *types.Package
	if pkgInScope.Path() == importedPkg {
		pkg = pkgInScope
	} else {
		for _, maybePkg := range pkgInScope.Imports() {
			if maybePkg.Path() == importedPkg {
				pkg = maybePkg
				break
			}
		}
	}
	return pkg, p.PackageNonDeterminisms(pkg)
}
