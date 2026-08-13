// Package testsuiteassertions checks testify suites for stale embedded require assertions.
// Place //testsuiteassertions:ignore followed by a reason on a suite type declaration
// when its assertions are intentionally suite-scoped.
package testsuiteassertions

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	requirePackage = "github.com/stretchr/testify/require"
	suitePackage   = "github.com/stretchr/testify/suite"
	ignorePrefix   = "//testsuiteassertions:ignore "
)

// Analyzer checks that suites embedding require.Assertions refresh it in SetupTest.
var Analyzer = &analysis.Analyzer{
	Name: "testsuiteassertions",
	Doc:  "check that testify suites refresh embedded require assertions for each test",
	Run:  run,
}

type suiteType struct {
	spec            *ast.TypeSpec
	assertionsField *types.Var
	ignored         bool
	setupTest       *ast.FuncDecl
}

// run reports testify suites that do not refresh embedded require assertions for each test.
func run(pass *analysis.Pass) (any, error) {
	suites := make(map[*types.TypeName]*suiteType)
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, rawSpec := range genDecl.Specs {
				spec, ok := rawSpec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				named, _ := types.Unalias(pass.TypesInfo.TypeOf(spec.Name)).(*types.Named)
				if named == nil {
					continue
				}
				structType, _ := named.Underlying().(*types.Struct)
				if structType == nil {
					continue
				}
				var assertionsField *types.Var
				hasSuite := false
				for field := range structType.Fields() {
					if !field.Embedded() {
						continue
					}
					switch {
					case isNamed(field.Type(), requirePackage, "Assertions"):
						assertionsField = field
					case isNamed(field.Type(), suitePackage, "Suite"):
						hasSuite = true
					}
				}
				if assertionsField != nil && hasSuite {
					ignored := hasIgnoreDirective(spec.Doc) || hasIgnoreDirective(spec.Comment)
					if len(genDecl.Specs) == 1 {
						ignored = ignored || hasIgnoreDirective(genDecl.Doc)
					}
					suites[named.Obj()] = &suiteType{
						spec:            spec,
						assertionsField: assertionsField,
						ignored:         ignored,
					}
				}
			}
		}
	}

	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "SetupTest" {
				continue
			}
			typeName, receiver := receiverType(pass, fn)
			if suite := suites[typeName]; suite != nil {
				suite.setupTest = fn
				if !suite.ignored && !rebindsAssertions(pass, fn, receiver, suite.assertionsField) {
					receiverName := "receiver"
					if receiver != nil {
						receiverName = receiver.Name()
					}
					pass.Reportf(fn.Name.Pos(), "%s.SetupTest must rebind embedded require.Assertions with require.New(%s.T())", typeName.Name(), receiverName)
				}
			}
		}
	}

	for typeName, suite := range suites {
		if !suite.ignored && suite.setupTest == nil {
			pass.Reportf(suite.spec.Name.Pos(), "%s embeds require.Assertions and suite.Suite; add SetupTest to rebind assertions with require.New(receiver.T())", typeName.Name())
		}
	}
	return nil, nil
}

func isNamed(typ types.Type, packagePath, name string) bool {
	if pointer, ok := types.Unalias(typ).(*types.Pointer); ok {
		typ = pointer.Elem()
	}
	named, _ := types.Unalias(typ).(*types.Named)
	return named != nil && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == packagePath && named.Obj().Name() == name
}

func receiverType(pass *analysis.Pass, fn *ast.FuncDecl) (*types.TypeName, *types.Var) {
	field := fn.Recv.List[0]
	var receiver *types.Var
	if len(field.Names) != 0 {
		receiver, _ = pass.TypesInfo.ObjectOf(field.Names[0]).(*types.Var)
	}
	typ := types.Unalias(pass.TypesInfo.TypeOf(field.Type))
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, _ := typ.(*types.Named)
	if named == nil {
		return nil, receiver
	}
	return named.Obj(), receiver
}

func rebindsAssertions(pass *analysis.Pass, fn *ast.FuncDecl, receiver, assertionsField *types.Var) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			if i >= len(assign.Rhs) || !isAssertionsSelector(pass, lhs, receiver, assertionsField) {
				continue
			}
			if isRequireNewForCurrentTest(pass, assign.Rhs[i], receiver) {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func isAssertionsSelector(pass *analysis.Pass, expr ast.Expr, receiver, assertionsField *types.Var) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || pass.TypesInfo.Selections[selector] == nil || pass.TypesInfo.Selections[selector].Obj() != assertionsField {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && pass.TypesInfo.ObjectOf(ident) == receiver
}

func isRequireNewForCurrentTest(pass *analysis.Pass, expr ast.Expr, receiver *types.Var) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	newSelector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	newFunc, _ := pass.TypesInfo.ObjectOf(newSelector.Sel).(*types.Func)
	if newFunc == nil || newFunc.Pkg() == nil || newFunc.Pkg().Path() != requirePackage || newFunc.Name() != "New" {
		return false
	}
	tCall, ok := call.Args[0].(*ast.CallExpr)
	if !ok || len(tCall.Args) != 0 {
		return false
	}
	tSelector, ok := tCall.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	selection := pass.TypesInfo.Selections[tSelector]
	if selection == nil {
		return false
	}
	tMethod, _ := selection.Obj().(*types.Func)
	if tMethod == nil || tMethod.Pkg() == nil || tMethod.Pkg().Path() != suitePackage || tMethod.Name() != "T" {
		return false
	}
	ident, ok := tSelector.X.(*ast.Ident)
	return ok && pass.TypesInfo.ObjectOf(ident) == receiver
}

func hasIgnoreDirective(group *ast.CommentGroup) bool {
	if group == nil {
		return false
	}
	for _, comment := range group.List {
		if strings.HasPrefix(comment.Text, ignorePrefix) && strings.TrimSpace(strings.TrimPrefix(comment.Text, ignorePrefix)) != "" {
			return true
		}
	}
	return false
}
