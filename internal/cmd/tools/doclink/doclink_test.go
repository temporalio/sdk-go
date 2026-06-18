package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsValidDefinitionWithMatchGroupedInterface(t *testing.T) {
	t.Parallel()

	if !isValidDefinitionWithMatch("ContextPropagator interface {", "ContextPropagator", "type", false) {
		t.Fatal("expected grouped interface declaration to match exposed internal type")
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
	if err := file.Close(); err != nil {
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
	if err := file.Close(); err != nil {
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
