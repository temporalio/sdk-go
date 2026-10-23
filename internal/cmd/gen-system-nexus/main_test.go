package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestSystemNexusGeneratorOptions(t *testing.T) {
	t.Setenv("GOWORK", "off")
	sdk := t.TempDir()
	writeTestFiles(t, sdk, map[string]string{
		"go.mod":                         "module example.com/sdk\n\ngo 1.26.0\n\nrequire go.temporal.io/api v1.64.0\nreplace go.temporal.io/api => ./api\n",
		"api/go.mod":                     "module go.temporal.io/api\n\ngo 1.26.0\n",
		"api/systemnexus/" + witFilePath: "upstream WIT",
		"workflow/placeholder.go":        "package workflow\n",
	})
	for _, tt := range []struct {
		name     string
		args     []string
		revision string
		preserve bool
		wantErr  string
	}{
		{name: "default", revision: nexGenRevision},
		{
			name:     "explicit revision",
			args:     []string{"--nexgen-rev", strings.Repeat("a", 40), "--preserve-build-dir"},
			revision: strings.Repeat("a", 40),
			preserve: true,
		},
		{name: "branch", args: []string{"--nexgen-rev", "main"}, wantErr: "full 40-character Git commit SHA"},
		{name: "short revision", args: []string{"--nexgen-rev", "c03f1df5"}, wantErr: "full 40-character Git commit SHA"},
		{name: "nonhex revision", args: []string{"--nexgen-rev", strings.Repeat("z", 40)}, wantErr: "full 40-character Git commit SHA"},
		{name: "extra argument", args: []string{"extra"}, wantErr: "unexpected arguments"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options, err := parseOptions(tt.args, sdk)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseOptions error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if options.nexGenRevision != tt.revision || options.preserveBuildDir != tt.preserve {
				t.Fatalf("options = %+v, want revision %q and preserve %v", options, tt.revision, tt.preserve)
			}
			if want := filepath.Join(sdk, "api", "systemnexus"); options.witDir != want {
				t.Fatalf("WIT directory = %q, want replacement module directory %q", options.witDir, want)
			}
			if want := filepath.Join(sdk, filepath.FromSlash(witOverridesPath)); options.witOverridesDir != want {
				t.Fatalf("override directory = %q, want %q", options.witOverridesDir, want)
			}
			if want := filepath.Join(sdk, dstPkgPath); options.dstPkgDir != want {
				t.Fatalf("destination directory = %q, want %q", options.dstPkgDir, want)
			}
		})
	}
}

func TestSystemNexusGeneratorWIT(t *testing.T) {
	for _, withOverrides := range []bool{false, true} {
		name := "upstream only"
		if withOverrides {
			name = "local overrides"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			overrides := filepath.Join(root, "overrides")
			build := filepath.Join(root, "build")
			original := map[string]string{
				witFilePath:                           "upstream service",
				"deps/nexus-temporal-types/model.wit": "upstream model",
				"deps/other/model.wit":                "unmodified dependency",
			}
			writeTestFiles(t, source, original)
			want := map[string]string{}
			for path, content := range original {
				want[path] = content
			}
			if withOverrides {
				patches := map[string]string{
					witFilePath:                           "local service",
					"deps/nexus-temporal-types/model.wit": "local model",
					"deps/new/model.wit":                  "new dependency",
				}
				writeTestFiles(t, overrides, patches)
				for path, content := range patches {
					want[path] = content
				}
			}
			if err := stageWIT(source, overrides, build); err != nil {
				t.Fatal(err)
			}
			checkTestFiles(t, build, want)
			checkTestFiles(t, source, original)
		})
	}
}

func TestSystemNexusGeneratorRejectsInvalidWITOverride(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	overrides := filepath.Join(root, "overrides")
	writeTestFiles(t, source, map[string]string{witFilePath: "upstream service"})
	writeTestFiles(t, overrides, map[string]string{"unexpected.txt": "not WIT"})
	err := stageWIT(source, overrides, filepath.Join(root, "build"))
	if err == nil || !strings.Contains(err.Error(), "must be a regular .wit file") {
		t.Fatalf("stageWIT error = %v, want invalid override error", err)
	}
}

func TestSystemNexusGeneratorDescriptors(t *testing.T) {
	path, err := genProtoDescriptors(filepath.Join(t.TempDir(), "descriptors"))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(contents, &set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		t.Fatalf("descriptor set has missing imports or duplicate files: %v", err)
	}
	if _, err := files.FindDescriptorByName("temporal.api.workflowservice.v1.SignalWithStartWorkflowExecutionRequest"); err != nil {
		t.Fatal(err)
	}
}

func TestSystemNexusGeneratorFormatting(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input", "workflowservice.go")
	writeTestFiles(t, root, map[string]string{
		"input/workflowservice.go": "package workflow\nvar value=1\n",
	})
	output, err := formatAndCopyFile(root, input)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "workflowservice.go"); output != want {
		t.Fatalf("output path = %q, want %q", output, want)
	}
	checkTestFiles(t, root, map[string]string{
		"workflowservice.go": "package workflow\n\nvar value = 1\n",
	})
	writeTestFiles(t, root, map[string]string{"input/workflowservice.go": "not Go"})
	if _, err := formatAndCopyFile(root, input); err == nil {
		t.Fatal("invalid Go source was accepted")
	}
	checkTestFiles(t, root, map[string]string{
		"workflowservice.go": "package workflow\n\nvar value = 1\n",
	})
}

func writeTestFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		dst := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func checkTestFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, want := range files {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != want {
			t.Errorf("%s = %q, want %q", path, content, want)
		}
	}
}
