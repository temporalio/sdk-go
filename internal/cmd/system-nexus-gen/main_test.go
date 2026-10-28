package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func newTestApp(t *testing.T) app {
	t.Helper()
	root := t.TempDir()
	a := app{
		sdk: sdkDir(filepath.Join(root, "sdk")),
		api: apiDir(filepath.Join(root, "api")),
		tmp: buildDir(filepath.Join(root, "build")),
	}
	for _, dir := range []string{a.api.witDir(), a.sdk.witOverridesDir(), string(a.tmp)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireTestFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

// -----------------------------------------------------------------------------
// WIT STAGING
// -----------------------------------------------------------------------------

func TestStageWitFilesWithOverrides(t *testing.T) {
	a := newTestApp(t)
	upstream := filepath.Join(a.api.witDir(), "service.wit")
	unchanged := filepath.Join(a.api.witDir(), "unchanged.wit")
	override := filepath.Join(a.sdk.witOverridesDir(), "service.wit")
	writeTestFile(t, upstream, "upstream")
	writeTestFile(t, unchanged, "unchanged")
	writeTestFile(t, override, "override")
	writeTestFile(t, filepath.Join(a.sdk.witOverridesDir(), "nested", "new.wit"), "new")
	writeTestFile(t, filepath.Join(a.sdk.witOverridesDir(), ".gitkeep"), "")

	if err := a.stageWitFilesWithOverrides(); err != nil {
		t.Fatal(err)
	}

	requireTestFile(t, filepath.Join(a.tmp.witDir(), "service.wit"), "override")
	requireTestFile(t, filepath.Join(a.tmp.witDir(), "unchanged.wit"), "unchanged")
	requireTestFile(t, filepath.Join(a.tmp.witDir(), "nested", "new.wit"), "new")
	if _, err := os.Stat(filepath.Join(a.tmp.witDir(), ".gitkeep")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged .gitkeep: %v, want not found", err)
	}
	requireTestFile(t, upstream, "upstream")
	requireTestFile(t, unchanged, "unchanged")
	requireTestFile(t, override, "override")
}

// -----------------------------------------------------------------------------
// PROTO DESCRIPTORS
// -----------------------------------------------------------------------------

func TestGenProtoDescriptors(t *testing.T) {
	a := app{tmp: buildDir(t.TempDir())}
	if err := a.genProtoDescriptors(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(a.tmp.descriptorsFile())
	if err != nil {
		t.Fatal(err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(contents, &set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.FindDescriptorByName("temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest"); err != nil {
		t.Fatal(err)
	}
}
