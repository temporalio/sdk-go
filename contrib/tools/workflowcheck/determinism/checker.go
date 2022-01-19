package determinism

import (
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

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

// Result is the result from Run and the result on the Analyzer.
type Result struct {
	// Only includes top-level functions
	Funcs map[*types.Func]NonDeterminisms
}

// Dump returns the result as a set of lines.
func (r *Result) Dump(includePos bool) (lines []string) {
	// Get func types and sort first for determinism
	funcTypes := make([]*types.Func, 0, len(r.Funcs))
	for funcType := range r.Funcs {
		funcTypes = append(funcTypes, funcType)
	}
	sort.Slice(funcTypes, func(i, j int) bool { return funcTypes[i].FullName() < funcTypes[j].FullName() })
	// Build lines
	for _, funcType := range funcTypes {
		lines = r.Funcs[funcType].AppendChildReasonLines(funcType.FullName(), lines, 0, includePos)
	}
	return
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
		ResultType: reflect.TypeOf((*Result)(nil)),
		FactTypes:  []analysis.Fact{&NonDeterminisms{}},
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

// Run executes this checker for the given pass and returns the result.
func (c *Checker) Run(pass *analysis.Pass) (*Result, error) {
	c.debugf("Checking package %v", pass.Pkg.Path())
	// Collect all non-determinisms in the package
	res := &Result{Funcs: map[*types.Func]NonDeterminisms{}}
	c.findNonDeterminisms(pass, res)
	return res, nil
}

func (c *Checker) findNonDeterminisms(pass *analysis.Pass, res *Result) {
	// Collect all top-level func decls and their types. Also mark var decls as
	// non-deterministic if pattern matches.
	funcDecls := map[*types.Func]*ast.FuncDecl{}
	ignoreMap := map[ast.Node]struct{}{}
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
		updateIgnoreMap(pass.Fset, file, ignoreMap)

		// Collect the decls to check
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
					if valueSpec, _ := spec.(*ast.ValueSpec); valueSpec != nil {
						for _, varName := range valueSpec.Names {
							if varType, _ := pass.TypesInfo.ObjectOf(varName).(*types.Var); varType != nil && varType.Pkg() != nil {
								fullName := varType.Pkg().Path() + "." + varType.Name()
								if c.IdentRefs[fullName] {
									c.debugf("Marking %v as non-deterministic because it matched a pattern", fullName)
									pos := pass.Fset.Position(varType.Pos())
									reasons := NonDeterminisms{&ReasonDecl{reasonBase: reasonBase{&pos}}}
									pass.ExportObjectFact(varType, &reasons)
								}
							}
						}
					}
				}
			}
		}
	}
	// Walk the decls capturing non-deterministic ones
	parents := map[*types.Func]bool{}
	for funcType := range funcDecls {
		c.applyNonDeterminisms(pass, funcType, funcDecls, ignoreMap, parents, res.Funcs)
	}
	// Set non-empty non-determinisms as facts
	for funcType, nonDet := range res.Funcs {
		nonDet := nonDet
		if len(nonDet) > 0 {
			pass.ExportObjectFact(funcType, &nonDet)
		}
	}
}

// Returns true if non-deterministic, false otherwise. Parents must be non-nil
// and all values must be true.
func (c *Checker) applyNonDeterminisms(
	pass *analysis.Pass,
	fn *types.Func,
	packageDecls map[*types.Func]*ast.FuncDecl,
	ignoreMap map[ast.Node]struct{},
	parents map[*types.Func]bool,
	results map[*types.Func]NonDeterminisms,
) NonDeterminisms {
	// Check to make sure not recursive
	if parents[fn] {
		// Recursive call is not marked non-deterministic
		return nil
	}
	// Check if determinisms already set or it's in a different package (which
	// means we can't re-set later)
	reasons, alreadySet := results[fn]
	if alreadySet || pass.ImportObjectFact(fn, &reasons) || fn.Pkg() != pass.Pkg {
		return reasons
	}
	// Check if matches pattern
	var skip bool
	if match, ok := c.IdentRefs[fn.FullName()]; match {
		c.debugf("Marking %v as non-deterministic because it matched a pattern", fn.FullName())
		pos := pass.Fset.Position(fn.Pos())
		reasons = append(reasons, &ReasonDecl{reasonBase: reasonBase{&pos}})
	} else if ok && !match {
		c.debugf("Skipping %v because it matched a pattern", fn.FullName())
		skip = true
	}
	// If not skipped and has top-level decl, walk the declaration body checking
	// for non-determinism
	if !skip && packageDecls[fn] != nil {
		ast.Inspect(packageDecls[fn], func(n ast.Node) bool {
			// Go no deeper if ignoring
			if _, ignored := ignoreMap[n]; ignored {
				return false
			}

			switch n := n.(type) {
			case *ast.CallExpr:
				// Check if the call is on a non-deterministic
				if callee, _ := typeutil.Callee(pass.TypesInfo, n).(*types.Func); callee != nil {
					// Put self on parents, then remove
					parents[fn] = true
					calleeNonDet := c.applyNonDeterminisms(pass, callee, packageDecls, ignoreMap, parents, results)
					delete(parents, fn)
					// If the callee is non-deterministic, mark this as such
					if len(calleeNonDet) > 0 {
						c.debugf("Marking %v as non-deterministic because it calls %v", fn.FullName(), callee.FullName())
						pos := pass.Fset.Position(n.Pos())
						reasons = append(reasons, &ReasonFuncCall{reasonBase: reasonBase{&pos}, Func: callee, Child: calleeNonDet})
					}
				}
			case *ast.GoStmt:
				// Any go statement is non-deterministic
				c.debugf("Marking %v as non-deterministic because it starts a goroutine", fn.FullName())
				pos := pass.Fset.Position(n.Pos())
				reasons = append(reasons, &ReasonConcurrency{reasonBase: reasonBase{&pos}, Kind: ConcurrencyKindGo})
			case *ast.Ident:
				// Check if ident is for a non-deterministic var
				if varType, _ := pass.TypesInfo.ObjectOf(n).(*types.Var); varType != nil {
					var ignore NonDeterminisms
					if pass.ImportObjectFact(varType, &ignore) {
						c.debugf("Marking %v as non-deterministic because it accesses %v.%v",
							fn.FullName(), varType.Pkg().Path(), varType.Name())
						pos := pass.Fset.Position(n.Pos())
						reasons = append(reasons, &ReasonVarAccess{reasonBase: reasonBase{&pos}, Var: varType})
					}
				}
			case *ast.RangeStmt:
				// Map and chan ranges are non-deterministic
				rangeType := pass.TypesInfo.TypeOf(n.X)
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
					c.debugf("Marking %v as non-deterministic because it iterates over a map", fn.FullName())
					pos := pass.Fset.Position(n.Pos())
					reasons = append(reasons, &ReasonMapRange{reasonBase: reasonBase{&pos}})
				case *types.Chan:
					c.debugf("Marking %v as non-deterministic because it iterates over a channel", fn.FullName())
					pos := pass.Fset.Position(n.Pos())
					reasons = append(reasons, &ReasonConcurrency{reasonBase: reasonBase{&pos}, Kind: ConcurrencyKindRange})
				}
			case *ast.SendStmt:
				// Any send statement is non-deterministic
				c.debugf("Marking %v as non-deterministic because it sends to a channel", fn.FullName())
				pos := pass.Fset.Position(n.Pos())
				reasons = append(reasons, &ReasonConcurrency{reasonBase: reasonBase{&pos}, Kind: ConcurrencyKindSend})
			case *ast.UnaryExpr:
				// If the operator is a receive, it is non-deterministic
				if n.Op == token.ARROW {
					c.debugf("Marking %v as non-deterministic because it receives from a channel", fn.FullName())
					pos := pass.Fset.Position(n.Pos())
					reasons = append(reasons, &ReasonConcurrency{reasonBase: reasonBase{&pos}, Kind: ConcurrencyKindRecv})
				}
			}
			return true
		})
	}
	// Put the reasons fact on the func, even if it is empty
	results[fn] = reasons
	return reasons
}

func updateIgnoreMap(fset *token.FileSet, f *ast.File, m map[ast.Node]struct{}) {
	// Collect only the ignore comments
	var comments []*ast.CommentGroup
	for _, group := range f.Comments {
		if len(group.List) == 1 && strings.HasPrefix(group.List[0].Text, "//workflowcheck:ignore") {
			comments = append(comments, group)
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
