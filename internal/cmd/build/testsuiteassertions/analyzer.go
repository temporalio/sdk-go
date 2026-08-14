// Package testsuiteassertions checks that testify suites refresh embedded require
// assertions when SetupTest begins. Nested Suite.Run transitions are outside the
// scope of this analyzer.
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
	requirePackage  = "github.com/stretchr/testify/require"
	suitePackage    = "github.com/stretchr/testify/suite"
	ignoreDirective = "//testsuiteassertions:ignore"
)

// Analyzer checks that suites embedding require.Assertions refresh it in SetupTest.
var Analyzer = &analysis.Analyzer{
	Name: "testsuiteassertions",
	Doc:  "check that testify suites refresh embedded require assertions in SetupTest",
	Run:  run,
}

type suiteType struct {
	spec            *ast.TypeSpec
	assertionsField *types.Var
	ignored         bool
	setupTest       *ast.FuncDecl
}

// run reports testify suites that do not refresh embedded require assertions in SetupTest.
func run(pass *analysis.Pass) (any, error) {
	suites := make(map[*types.TypeName]*suiteType)
	// Pass 1: collect affected suite declarations across the package. SetupTest
	// may be declared in another file, so discovery must finish before validation.
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
					commentGroups := []*ast.CommentGroup{spec.Doc, spec.Comment}
					if len(genDecl.Specs) == 1 {
						commentGroups = append(commentGroups, genDecl.Doc)
					}
					ignored, invalidIgnore := findIgnoreDirective(commentGroups...)
					suites[named.Obj()] = &suiteType{
						spec:            spec,
						assertionsField: assertionsField,
						ignored:         ignored || invalidIgnore.IsValid(),
					}
					if invalidIgnore.IsValid() {
						pass.Reportf(spec.Name.Pos(), "%s requires a reason", ignoreDirective)
					}
				}
			}
		}
	}

	// Pass 2: match SetupTest methods to the collected suite types, then validate
	// each hook's signature and first-statement assertion rebinding.
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "SetupTest" {
				continue
			}
			typeName, receiver := receiverType(pass, fn)
			if suite := suites[typeName]; suite != nil {
				suite.setupTest = fn
				if !suite.ignored && !isSetupTestHook(pass, fn) {
					pass.Reportf(fn.Name.Pos(), "%s.SetupTest must have signature func (*%s) SetupTest()", typeName.Name(), typeName.Name())
					continue
				}
				if !suite.ignored && !rebindsAssertions(pass, fn, receiver, suite.assertionsField) {
					receiverName := "receiver"
					if receiver != nil {
						receiverName = receiver.Name()
					}
					pass.Reportf(fn.Name.Pos(), "%s.SetupTest must rebind embedded require.Assertions with require.New(%s.T()) as its first statement", typeName.Name(), receiverName)
				}
			}
		}
	}

	// Pass 3: after every method has been seen, report affected suites that did
	// not declare SetupTest anywhere in the package.
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

func isSetupTestHook(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	method, _ := pass.TypesInfo.ObjectOf(fn.Name).(*types.Func)
	if method == nil {
		return false
	}
	signature, _ := method.Type().(*types.Signature)
	if signature == nil || signature.Recv() == nil || signature.Params().Len() != 0 || signature.Results().Len() != 0 {
		return false
	}
	_, ok := types.Unalias(signature.Recv().Type()).(*types.Pointer)
	return ok
}

func rebindsAssertions(pass *analysis.Pass, fn *ast.FuncDecl, receiver, assertionsField *types.Var) bool {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	assign, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok {
		return false
	}
	for i, lhs := range assign.Lhs {
		if i < len(assign.Rhs) &&
			isAssertionsSelector(pass, lhs, receiver, assertionsField) &&
			isRequireNewForCurrentTest(pass, assign.Rhs[i], receiver) {
			return true
		}
	}
	return false
}

func isAssertionsSelector(pass *analysis.Pass, expr ast.Expr, receiver, assertionsField *types.Var) bool {
	selector, ok := ast.Unparen(expr).(*ast.SelectorExpr)
	if !ok || pass.TypesInfo.Selections[selector] == nil || pass.TypesInfo.Selections[selector].Obj() != assertionsField {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && pass.TypesInfo.ObjectOf(ident) == receiver
}

func isRequireNewForCurrentTest(pass *analysis.Pass, expr ast.Expr, receiver *types.Var) bool {
	expr = ast.Unparen(expr)
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(star.X)
	}
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

func findIgnoreDirective(groups ...*ast.CommentGroup) (valid bool, invalid token.Pos) {
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, ignoreDirective) {
				continue
			}
			reason := strings.TrimPrefix(comment.Text, ignoreDirective)
			if reason != "" && !strings.HasPrefix(reason, " ") && !strings.HasPrefix(reason, "\t") {
				continue
			}
			if strings.TrimSpace(reason) != "" {
				valid = true
			} else if !invalid.IsValid() {
				invalid = comment.Pos()
			}
		}
	}
	if valid {
		invalid = token.NoPos
	}
	return valid, invalid
}
