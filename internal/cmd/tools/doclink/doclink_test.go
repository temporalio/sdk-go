package main

import (
	"go/ast"
	"go/parser"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIsValidDefinitionWithMatchGroupedInterface(t *testing.T) {
	t.Parallel()

	if !isValidDefinitionWithMatch("ContextPropagator interface {", "ContextPropagator", "type", false) {
		t.Fatal("expected grouped interface declaration to match exposed internal type")
	}
}

func TestSupportsGoSyntax(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "future.go")
	if err := os.WriteFile(path, []byte("//go:build go1.27\n\npackage future\n"), 0644); err != nil {
		t.Fatal(err)
	}

	supported, err := supportsGoSyntax(path, "go1.26")
	if err != nil {
		t.Fatal(err)
	}
	if supported {
		t.Fatal("expected future Go syntax to be skipped")
	}
}

func TestIsValidDefinitionWithMatchGroupedNamedType(t *testing.T) {
	t.Parallel()

	if !isValidDefinitionWithMatch("SessionState int", "SessionState", "type", false) {
		t.Fatal("expected grouped named type declaration to match exposed internal type")
	}
}

func TestIsValidDefinitionWithMatchSkipsEmbeddedInterfaceMember(t *testing.T) {
	t.Parallel()

	if isValidDefinitionWithMatch("SendChannel", "SendChannel", "type", false) {
		t.Fatal("expected embedded interface member not to match exposed internal type")
	}
}

func TestIsValidDefinitionWithMatchSkipsCommentedEmbeddedInterfaceMember(t *testing.T) {
	t.Parallel()

	if isValidDefinitionWithMatch("SendChannel // embedded channel", "SendChannel", "type", false) {
		t.Fatal("expected commented embedded interface member not to match exposed internal type")
	}
}

func TestProcessInternalAddsDocLinkForGroupedInterface(t *testing.T) {
	oldChangesNeeded := changesNeeded
	changesNeeded = false
	t.Cleanup(func() {
		changesNeeded = oldChangesNeeded
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "headers.go")
	source := `package internal

type (
	// ContextPropagator is an interface that determines what information from
	// context to pass along.
	ContextPropagator interface {
		Inject() error
	}
)
`
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	pairs := map[string]map[string]string{
		"workflow": {
			"ContextPropagator": "ContextPropagator",
		},
	}
	if err := processInternal(config{fix: true}, file, pairs); err != nil {
		t.Fatal(err)
	}

	updatedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(updatedBytes)
	want := "// Exposed as: [go.temporal.io/sdk/workflow.ContextPropagator]"
	if !strings.Contains(updated, want) {
		t.Fatalf("expected generated doc link %q in:\n%s", want, updated)
	}
}

func TestProcessInternalDoesNotAddDocLinkForCommentedEmbeddedInterfaceMember(t *testing.T) {
	oldChangesNeeded := changesNeeded
	changesNeeded = false
	t.Cleanup(func() {
		changesNeeded = oldChangesNeeded
	})

	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.go")
	source := `package internal

type (
	// Channel allows sending and receiving values.
	Channel interface {
		SendChannel // embedded write-only view
	}
)
`
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	pairs := map[string]map[string]string{
		"workflow": {
			"SendChannel": "SendChannel",
		},
	}
	if err := processInternal(config{fix: true}, file, pairs); err != nil {
		t.Fatal(err)
	}

	updatedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := string(updatedBytes)
	unwanted := "// Exposed as: [go.temporal.io/sdk/workflow.SendChannel]"
	if strings.Contains(updated, unwanted) {
		t.Fatalf("did not expect generated doc link %q in:\n%s", unwanted, updated)
	}
}

func TestExtractTypeValueGeneric(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{expression: "internal.LocalVar[T]", want: "LocalVar"},
		{expression: "internal.Pair[K, V]", want: "Pair"},
	}
	for _, test := range tests {
		expr, err := parser.ParseExpr(test.expression)
		if err != nil {
			t.Fatal(err)
		}
		if value := (internalPackages{"internal": ""}).extractTypeValue(expr); value != test.want {
			t.Fatalf("extractTypeValue(%q) = %q, want %q", test.expression, value, test.want)
		}
	}
}

func TestIsInternalFunctionCallGeneric(t *testing.T) {
	expr, err := parser.ParseExpr("internal.NewLocalVar[T](ctx)")
	if err != nil {
		t.Fatal(err)
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("parsed expression is %T, want *ast.CallExpr", expr)
	}
	if value := (internalPackages{"internal": ""}).isInternalFunctionCall(call); value != "NewLocalVar" {
		t.Fatalf("isInternalFunctionCall() = %q, want NewLocalVar", value)
	}
}

func TestNestedInternalPackage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := map[string]string{
		"converter/public.go": `package converter
import (
	commonconverter "go.temporal.io/sdk/internal/common/converter"
	renamed "go.temporal.io/sdk/internal"
	other "example.com/other"
)
type DataConverter = commonconverter.DataConverter
type RootConverter = renamed.DataConverter
type Unrelated = other.DataConverter
func NewTransferTypeConverter[T any]() DataConverter {
	return commonconverter.NewTransferTypeConverter[T]()
}
`,
		"internal/root.go": `package internal
// DataConverter is the root converter.
//
// Exposed as: [go.temporal.io/sdk/converter.RootConverter]
type DataConverter interface {}
`,
		"internal/common/converter/converter.go": `package converter
// DataConverter converts payloads.
type DataConverter interface {}
// NewTransferTypeConverter creates a converter.
func NewTransferTypeConverter[T any]() DataConverter { return nil }
`,
	}
	for path, source := range files {
		path = filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(filepath.Join(dir, "converter/public.go"))
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := processPublic(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DataConverter":            "/common/converter.DataConverter",
		"RootConverter":            "DataConverter",
		"NewTransferTypeConverter": "/common/converter.NewTransferTypeConverter",
	}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("processPublic() = %v, want %v", pairs, want)
	}

	cmd := exec.Command("go", "run", "doclink.go", "-rootDir", dir)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "Missing doc") {
		t.Fatalf("check without links: error = %v, output:\n%s", err, output)
	}
	cmd = exec.Command("go", "run", "doclink.go", "-rootDir", dir, "-fix")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fix: %v\n%s", err, output)
	}
	cmd = exec.Command("go", "run", "doclink.go", "-rootDir", dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check after fix: %v\n%s", err, output)
	}
	for _, path := range []string{"internal/root.go", "internal/common/converter/converter.go"} {
		content, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		rootLink := strings.Contains(string(content), "[go.temporal.io/sdk/converter.RootConverter]")
		nestedLink := strings.Contains(string(content), "[go.temporal.io/sdk/converter.DataConverter]")
		if rootLink != (path == "internal/root.go") || nestedLink != (path != "internal/root.go") {
			t.Fatalf("links were associated with the wrong package in %s:\n%s", path, content)
		}
	}
}
