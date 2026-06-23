// Command nexussystemgen generates the system Nexus client bindings for the Go
// SDK by running nex-gen against the checked-in WIT and adapting the output so
// it can live inside the SDK without import cycles.
//
// nex-gen emits an `api.go` that targets the public `go.temporal.io/sdk/workflow`,
// `.../temporal`, and `.../client` packages. Those packages all depend on
// `go.temporal.io/sdk/internal`, so a generated package that the public
// `workflow` package re-exports cannot import them (workflow -> generated ->
// workflow would be a cycle). This tool rewrites the nex-gen output to target
// `go.temporal.io/sdk/internal` directly and places it in its own
// `internal/nexussystem` package, which `workflow` re-exports via a generated
// forwarder. The resulting import graph (workflow -> internal/nexussystem ->
// internal, and workflow -> internal) is acyclic.
//
// Usage:
//
//	NEX_GEN_BIN=/path/to/nex-gen \
//	  go run ./internal/cmd/nexussystemgen \
//	  -descriptors /path/to/descriptor_set.pb
//
// nex-gen is located via the NEX_GEN_BIN environment variable, falling back to
// a `nex-gen` binary on PATH.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	// internalImport is the SDK-internal package that re-defines the types the
	// generated bindings need (Context, NewNexusClient, RetryPolicy, ...).
	internalImport = "go.temporal.io/sdk/internal"
	// leafPackage is the package name for the generated bindings.
	leafPackage = "nexussystem"
)

// rewrittenPackages are the nex-gen import path -> local package name pairs
// that must be redirected to the SDK-internal package. The public
// workflow/temporal/client packages are all thin re-exports (type aliases /
// forwarding funcs) of internal, so rewriting their selector qualifiers to
// `internal` is semantically identity-preserving.
var rewrittenPackages = map[string]string{
	"go.temporal.io/sdk/workflow": "workflow",
	"go.temporal.io/sdk/temporal": "temporal",
	"go.temporal.io/sdk/client":   "client",
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("nexussystemgen: %v", err)
	}
}

func run() error {
	descriptors := flag.String("descriptors", "", "path to the proto descriptor set (FileDescriptorSet)")
	flag.Parse()
	if *descriptors == "" {
		return fmt.Errorf("-descriptors is required")
	}

	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	witDir := filepath.Join(repoRoot, "internal", "nexussystem", "wit")
	witMain := filepath.Join(witDir, "workflow-service.wit")
	witDeps := filepath.Join(witDir, "deps")
	outPkgDir := filepath.Join(repoRoot, "internal", "nexussystem")

	tmpDir, err := os.MkdirTemp("", "nexussystemgen-")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	nexGen := os.Getenv("NEX_GEN_BIN")
	if nexGen == "" {
		nexGen = "nex-gen"
	}

	cmd := exec.Command(nexGen, "generate",
		"--lang", "go",
		"--input", witMain,
		"--input", witDeps,
		"--descriptors", *descriptors,
		"--output", tmpDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nex-gen: %w", err)
	}

	// Adapt and write each generated Go file into the leaf package. The
	// dependency-free registry sub-package is not needed here (api-go owns the
	// payload-visitor registry), so it is ignored.
	for _, name := range []string{"api.go", "model_overrides.go"} {
		src := filepath.Join(tmpDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("reading generated %s: %w", name, err)
		}
		adapted, err := adaptGeneratedFile(string(data))
		if err != nil {
			return fmt.Errorf("adapting %s: %w", name, err)
		}
		dst := filepath.Join(outPkgDir, name)
		if err := os.WriteFile(dst, []byte(adapted), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dst, err)
		}
		if err := gofmt(dst); err != nil {
			return err
		}
	}

	// Generate the public re-export forwarder in the workflow package.
	forwarder := filepath.Join(repoRoot, "workflow", "system_nexus_gen.go")
	if err := os.WriteFile(forwarder, []byte(workflowForwarder()), 0o644); err != nil {
		return fmt.Errorf("writing forwarder: %w", err)
	}
	if err := gofmt(forwarder); err != nil {
		return err
	}

	return nil
}

// adaptGeneratedFile rewrites a nex-gen Go file so it targets the SDK-internal
// package. Using the Go AST (rather than text substitution) ensures only real
// package qualifiers are rewritten -- import path strings and string literals
// such as the proto-qualified ServiceName are left untouched.
//
// The transformation:
//   - renames the package to `nexussystem` (nex-gen derives an invalid package
//     name from the "__temporal_system" endpoint);
//   - removes the public workflow/temporal/client imports and adds the internal
//     import (named `internal`) when any of them was present;
//   - rewrites selector expressions `pkg.Sel` whose qualifier refers to one of
//     the removed packages so they reference `internal` instead.
func adaptGeneratedFile(content string) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", content, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parsing: %w", err)
	}

	file.Name = ast.NewIdent(leafPackage)

	// Determine the local names of the imports we are redirecting, and whether
	// any internal import already exists.
	redirectNames := map[string]bool{} // local name -> redirected
	needsInternal := false
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if _, ok := rewrittenPackages[path]; ok {
			name := importLocalName(imp, path)
			redirectNames[name] = true
			needsInternal = true
		}
	}

	// Rewrite selector qualifiers that reference a redirected package.
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if redirectNames[ident.Name] {
			ident.Name = "internal"
		}
		return true
	})

	// Rebuild the import declarations, dropping redirected imports and adding
	// the internal import once.
	rewriteImports(file, needsInternal)

	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return "", fmt.Errorf("printing: %w", err)
	}
	return buf.String(), nil
}

// importLocalName returns the local name an import is referenced by: its
// explicit alias when present, otherwise the package's base path segment.
func importLocalName(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	return filepath.Base(path)
}

// rewriteImports removes the redirected SDK imports from the file's import
// declarations and, if needed, appends the internal import.
func rewriteImports(file *ast.File, needsInternal bool) {
	internalAlreadyPresent := false
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.IMPORT {
			continue
		}
		kept := gen.Specs[:0]
		for _, spec := range gen.Specs {
			imp := spec.(*ast.ImportSpec)
			path, _ := strconv.Unquote(imp.Path.Value)
			if _, ok := rewrittenPackages[path]; ok {
				continue
			}
			if path == internalImport {
				internalAlreadyPresent = true
			}
			kept = append(kept, spec)
		}
		gen.Specs = kept
	}

	if needsInternal && !internalAlreadyPresent {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.IMPORT {
				continue
			}
			gen.Specs = append(gen.Specs, &ast.ImportSpec{
				Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(internalImport)},
			})
			break
		}
	}
}

// workflowForwarder returns the generated public re-export that surfaces the
// system Nexus operations on the workflow package.
func workflowForwarder() string {
	return `// Code generated by nexussystemgen. DO NOT EDIT.

package workflow

import (
	"go.temporal.io/sdk/internal/nexussystem"
)

// SignalWithStartWorkflowExecutionResponse is the result of a
// SignalWithStartWorkflow operation.
type SignalWithStartWorkflowExecutionResponse = nexussystem.SignalWithStartWorkflowResponse

// SignalWithStartWorkflowOptions configures a SignalWithStartWorkflow call.
type SignalWithStartWorkflowOptions = nexussystem.SignalWithStartWorkflowOptions

// SignalWithStartWorkflow signals a workflow, starting it first if needed, via
// the built-in system Nexus endpoint.
func SignalWithStartWorkflow(
	ctx Context,
	workflowID string,
	id string,
	taskQueue string,
	signal string,
	opts SignalWithStartWorkflowOptions,
) (*SignalWithStartWorkflowExecutionResponse, error) {
	return nexussystem.SignalWithStartWorkflow(ctx, workflowID, id, taskQueue, signal, opts)
}
`
}

func gofmt(path string) error {
	cmd := exec.Command("gofmt", "-w", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gofmt %s: %w", path, err)
	}
	return nil
}

func repoRoot() (string, error) {
	// This file lives at <repo>/internal/cmd/nexussystemgen/main.go.
	_, err := os.Stat("go.mod")
	if err == nil {
		abs, err := filepath.Abs(".")
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return "", fmt.Errorf("nexussystemgen must be run from the repository root (where go.mod lives)")
}
