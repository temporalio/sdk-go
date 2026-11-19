package internal

import (
	"runtime"
	"runtime/debug"
	"testing"

	workerpb "go.temporal.io/api/worker/v1"
)

func TestDetectEnvironmentInfoReportsGoRuntimeAndPlatform(t *testing.T) {
	t.Parallel()
	info := detectEnvironmentInfo()

	if len(info.GetRuntimes()) != 1 {
		t.Fatalf("runtimes = %v, want exactly one", info.GetRuntimes())
	}
	if got := info.GetRuntimes()[0].GetType(); got != workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_GO {
		t.Fatalf("runtime type = %v, want GO", got)
	}
	if got := info.GetRuntimes()[0].GetVersion(); got != runtime.Version() {
		t.Fatalf("runtime version = %q, want %q", got, runtime.Version())
	}

	var arch workerpb.EnvironmentInfo_Architecture
	switch runtime.GOOS {
	case "linux":
		arch = info.GetPlatform().GetLinux().GetArchitecture()
	case "darwin":
		arch = info.GetPlatform().GetMacos().GetArchitecture()
	case "windows":
		arch = info.GetPlatform().GetWindows().GetArchitecture()
	default:
		if info.GetPlatform() != nil {
			t.Fatalf("platform = %v, want nil on %s", info.GetPlatform(), runtime.GOOS)
		}
		return
	}
	want := workerpb.EnvironmentInfo_ARCHITECTURE_UNSPECIFIED
	switch runtime.GOARCH {
	case "amd64":
		want = workerpb.EnvironmentInfo_ARCHITECTURE_AMD64
	case "arm64":
		want = workerpb.EnvironmentInfo_ARCHITECTURE_ARM64
	}
	if arch != want {
		t.Fatalf("architecture = %v, want %v", arch, want)
	}
}

func TestDetectHostingEnvironments(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"KUBERNETES_SERVICE_HOST":     "10.0.0.1",
		"ECS_CONTAINER_METADATA_URI":  "http://169.254.170.2/v3",
		"WEBSITE_SITE_NAME":           "my-site",
		"WEBSITE_PLATFORM_VERSION":    " 1.2.3 ",
		"FUNCTIONS_EXTENSION_VERSION": "~4",
		"GAE_SERVICE":                 "   ",
	}
	lookup := func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
	got := detectHostingEnvironments(lookup, func() bool { return true })

	type hosting = workerpb.EnvironmentInfo_HostingEnvironment
	want := []*hosting{
		{Type: workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_DOCKER},
		{Type: workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_K8S},
		{Type: workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AWS_ECS},
		{Type: workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AZURE_APP_SERVICE, Version: "1.2.3"},
		{Type: workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AZURE_FUNCTIONS, Version: "~4"},
	}
	if len(got) != len(want) {
		t.Fatalf("hosting environments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].GetType() != want[i].GetType() || got[i].GetVersion() != want[i].GetVersion() {
			t.Fatalf("hosting environment %d = %v, want %v", i, got[i], want[i])
		}
	}

	if got := detectHostingEnvironments(func(string) (string, bool) { return "", false }, func() bool { return false }); len(got) != 0 {
		t.Fatalf("hosting environments = %v, want none", got)
	}
}

func TestCgroupsIndicateDocker(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"0::/":                                                       false,
		"12:pids:/docker/abc123\n0::/":                               true,
		"0::/system.slice/docker-abc123.scope":                       true,
		"0::/system.slice/docker-abc123.service":                     false,
		"0::/kubepods/besteffort/pod123/dockerish":                   false,
		"1:name=systemd:/user.slice/user-1000.slice/session-1.scope": false,
	}
	for cgroups, want := range cases {
		if got := cgroupsIndicateDocker(cgroups); got != want {
			t.Errorf("cgroupsIndicateDocker(%q) = %v, want %v", cgroups, got, want)
		}
	}
}

func TestDetectRuntimesRoadRunner(t *testing.T) {
	t.Parallel()
	goRuntime := workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_GO
	rrRuntime := workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_ROADRUNNER

	cases := []struct {
		name      string
		buildInfo *debug.BuildInfo
		want      []*workerpb.EnvironmentInfo_Runtime
	}{
		{
			name:      "no build info",
			buildInfo: nil,
			want:      []*workerpb.EnvironmentInfo_Runtime{{Type: goRuntime, Version: runtime.Version()}},
		},
		{
			name: "plain go program",
			buildInfo: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/worker", Version: "(devel)"},
				Deps: []*debug.Module{{Path: "go.temporal.io/sdk", Version: "v1.50.0"}},
			},
			want: []*workerpb.EnvironmentInfo_Runtime{{Type: goRuntime, Version: runtime.Version()}},
		},
		{
			name: "roadrunner release binary",
			buildInfo: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/roadrunner-server/roadrunner/v2025", Version: "v2025.1.2"},
				Deps: []*debug.Module{{Path: "github.com/temporalio/roadrunner-temporal/v6", Version: "v6.1.0"}},
			},
			want: []*workerpb.EnvironmentInfo_Runtime{
				{Type: goRuntime, Version: runtime.Version()},
				{Type: rrRuntime, Version: "v2025.1.2"},
			},
		},
		{
			name: "roadrunner built from source without tag",
			buildInfo: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/roadrunner-server/roadrunner/v2025", Version: "(devel)"},
			},
			want: []*workerpb.EnvironmentInfo_Runtime{
				{Type: goRuntime, Version: runtime.Version()},
				{Type: rrRuntime},
			},
		},
		{
			name: "fork embedding the temporal plugin",
			buildInfo: &debug.BuildInfo{
				Main: debug.Module{Path: "example.com/custom-rr", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: "github.com/temporalio/roadrunner-temporal/v6", Version: "v6.1.0"}},
			},
			want: []*workerpb.EnvironmentInfo_Runtime{
				{Type: goRuntime, Version: runtime.Version()},
				{Type: rrRuntime},
			},
		},
		{
			name: "similarly named module is not roadrunner",
			buildInfo: &debug.BuildInfo{
				Main: debug.Module{Path: "github.com/roadrunner-server/roadrunner-tools", Version: "v1.0.0"},
				Deps: []*debug.Module{{Path: "github.com/temporalio/roadrunner-temporal-docs", Version: "v1.0.0"}},
			},
			want: []*workerpb.EnvironmentInfo_Runtime{{Type: goRuntime, Version: runtime.Version()}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectRuntimes(tc.buildInfo)
			if len(got) != len(tc.want) {
				t.Fatalf("runtimes = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i].GetType() != tc.want[i].GetType() || got[i].GetVersion() != tc.want[i].GetVersion() {
					t.Fatalf("runtime %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
