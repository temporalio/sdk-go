package pure

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestListContribModules(t *testing.T) {
	paths := []string{
		"go.mod",
		"contrib/envconfig/go.mod",
		"contrib/envconfig/CHANGELOG.md",
		"contrib/aws/s3driver/go.mod",
		"contrib/aws/s3driver/awssdkv2/go.mod",
		"contrib/aws/s3driver/awssdkv2/CHANGELOG.md",
		"contrib/aws/s3driver/awssdkv2/internal/foo.go",
	}
	modules := ContribModules(paths)
	want := []Module{
		{Dir: "contrib/aws/s3driver"},
		{Dir: "contrib/aws/s3driver/awssdkv2"},
		{Dir: "contrib/envconfig"},
	}
	if !slices.Equal(modules, want) {
		t.Fatalf("unexpected modules: got %+v, want %+v", modules, want)
	}
}

func TestParseVersionedModules(t *testing.T) {
	args := []string{"sdk=1.48.0", "contrib/aws/s3driver/awssdkv2=0.2.0"}
	modules, err := ParseVersionedModules(args)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatVersionedModules(modules); got != strings.Join(args, " ") {
		t.Fatalf("unexpected versioned modules: %s", got)
	}
	if modules[0].Module != SDKModule() || modules[0].Version != (Version{1, 48, 0}) {
		t.Fatalf("unexpected versioned SDK module: %+v", modules[0])
	}
	if got := modules[1].Module.Tag(modules[1].Version); got != "contrib/aws/s3driver/awssdkv2/v0.2.0" {
		t.Fatalf("unexpected contrib tag: %s", got)
	}
	date := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	if got := ReleaseBranchName(date); got != "chore/release-2026-08-04" {
		t.Fatalf("unexpected branch: %s", got)
	}
	if got := ReleaseCommitMessage(modules, date); got != "Prepare release 2026-08-04\n\n"+strings.Join(args, " ") {
		t.Fatalf("unexpected commit message: %s", got)
	}
}

func TestParseVersionedModulesRejectsDuplicateModules(t *testing.T) {
	for _, args := range [][]string{
		{"sdk=1.48.0", "sdk=1.48.1"},
		{"contrib/envconfig=0.2.0", "./contrib/envconfig/=0.3.0"},
	} {
		if _, err := ParseVersionedModules(args); err == nil || !strings.Contains(err.Error(), "specified more than once") {
			t.Errorf("expected duplicate module error for %q, got %v", args, err)
		}
	}
}

func TestContribModuleRegexp(t *testing.T) {
	testRegexp(t, contribModuleRE,
		[]string{"contrib/envconfig", "contrib/opentelemetry-v2", "contrib/aws/s3driver/awssdkv2"},
		[]string{
			"contrib",
			"contrib/",
			"/contrib/envconfig",
			"contrib//envconfig",
			"contrib/../internal",
			"contrib/-envconfig",
			"contrib/Envconfig",
			"internal",
			"",
		})
}

func TestContribModuleRejectsInvalidModules(t *testing.T) {
	for _, dir := range []string{"", "contrib", "internal", "/contrib/envconfig", "../contrib/envconfig", "contrib/../internal", "contrib/envconfig/../tally"} {
		assertInvalidModule(t, dir)
	}
}

func TestContribModuleRejectsBackslashSeparators(t *testing.T) {
	for _, dir := range []string{`contrib\envconfig`, `.\contrib\envconfig`, `contrib\..\internal`, `C:\contrib\envconfig`} {
		assertInvalidModule(t, dir)
	}
}

func assertInvalidModule(t *testing.T, dir string) {
	t.Helper()
	if _, err := ContribModule(dir); err == nil || !strings.Contains(err.Error(), "invalid module") {
		t.Errorf("expected %q to be rejected, got %v", dir, err)
	}
}
