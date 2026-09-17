package internal

import (
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	workerpb "go.temporal.io/api/worker/v1"
)

const (
	// roadRunnerModulePrefix is the main module of every RoadRunner binary.
	roadRunnerModulePrefix = "github.com/roadrunner-server/roadrunner"
	// roadRunnerTemporalModulePrefix is the RoadRunner plugin through which the PHP SDK uses this SDK.
	roadRunnerTemporalModulePrefix = "github.com/temporalio/roadrunner-temporal"
)

// detectEnvironmentInfo collects the runtime, hosting environment, and platform information
// reported in the first accepted worker heartbeat.
func detectEnvironmentInfo() *workerpb.EnvironmentInfo {
	buildInfo, _ := debug.ReadBuildInfo()
	return &workerpb.EnvironmentInfo{
		Runtimes:            detectRuntimes(buildInfo),
		HostingEnvironments: detectHostingEnvironments(os.LookupEnv, isDocker),
		Platform:            detectPlatform(),
	}
}

// detectRuntimes always reports the Go runtime, and additionally RoadRunner when this SDK is
// linked into a RoadRunner binary (the PHP SDK's execution model). RoadRunner is identified from
// the binary's build info rather than from anything the PHP SDK passes in.
func detectRuntimes(buildInfo *debug.BuildInfo) []*workerpb.EnvironmentInfo_Runtime {
	runtimes := []*workerpb.EnvironmentInfo_Runtime{{
		Type:    workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_GO,
		Version: runtime.Version(),
	}}
	if version, ok := roadRunnerVersion(buildInfo); ok {
		runtimes = append(runtimes, &workerpb.EnvironmentInfo_Runtime{
			Type:    workerpb.EnvironmentInfo_Runtime_RUNTIME_TYPE_ROADRUNNER,
			Version: version,
		})
	}
	return runtimes
}

func roadRunnerVersion(buildInfo *debug.BuildInfo) (string, bool) {
	if buildInfo == nil {
		return "", false
	}
	if isModule(buildInfo.Main.Path, roadRunnerModulePrefix) {
		// "(devel)" is Go's placeholder for builds outside of a tagged module.
		if buildInfo.Main.Version == "(devel)" {
			return "", true
		}
		return buildInfo.Main.Version, true
	}
	for _, dep := range buildInfo.Deps {
		if isModule(dep.Path, roadRunnerTemporalModulePrefix) {
			// A RoadRunner binary built from a fork or vendored main module: RoadRunner is present,
			// but its version is not knowable from the plugin alone.
			return "", true
		}
	}
	return "", false
}

func isModule(path, prefix string) bool {
	rest, ok := strings.CutPrefix(path, prefix)
	return ok && (rest == "" || strings.HasPrefix(rest, "/"))
}

// detectHostingEnvironments identifies hosting environments from well-known indicators. Several
// environments may be detected at once, e.g. Docker inside Kubernetes or Azure Functions inside
// Azure App Service.
func detectHostingEnvironments(
	lookupEnv func(string) (string, bool),
	isDocker func() bool,
) []*workerpb.EnvironmentInfo_HostingEnvironment {
	envValue := func(name string) string {
		value, _ := lookupEnv(name)
		return strings.TrimSpace(value)
	}
	hasAnyEnv := func(names ...string) bool {
		for _, name := range names {
			if envValue(name) != "" {
				return true
			}
		}
		return false
	}

	var environments []*workerpb.EnvironmentInfo_HostingEnvironment
	add := func(t workerpb.EnvironmentInfo_HostingEnvironment_HostingEnvironmentType, version string) {
		environments = append(environments, &workerpb.EnvironmentInfo_HostingEnvironment{Type: t, Version: version})
	}

	if isDocker() {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_DOCKER, "")
	}
	if hasAnyEnv("KUBERNETES_SERVICE_HOST") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_K8S, "")
	}
	if hasAnyEnv("AWS_LAMBDA_FUNCTION_NAME") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AWS_LAMBDA, "")
	}
	if hasAnyEnv("ECS_CONTAINER_METADATA_URI_V4", "ECS_CONTAINER_METADATA_URI") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AWS_ECS, "")
	}
	if hasAnyEnv("K_SERVICE", "CLOUD_RUN_JOB", "CLOUD_RUN_WORKER_POOL") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_GOOGLE_CLOUD_RUN, "")
	}
	if hasAnyEnv("GAE_SERVICE") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_GOOGLE_APP_ENGINE, "")
	}
	if hasAnyEnv("WEBSITE_SITE_NAME") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AZURE_APP_SERVICE, envValue("WEBSITE_PLATFORM_VERSION"))
	}
	if version := envValue("FUNCTIONS_EXTENSION_VERSION"); version != "" {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AZURE_FUNCTIONS, version)
	}
	if hasAnyEnv("CONTAINER_APP_NAME", "CONTAINER_APP_JOB_NAME") {
		add(workerpb.EnvironmentInfo_HostingEnvironment_HOSTING_ENVIRONMENT_TYPE_AZURE_CONTAINER_APPS, "")
	}
	return environments
}

func isDocker() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	cgroups, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	return cgroupsIndicateDocker(string(cgroups))
}

// cgroupsIndicateDocker reports whether any cgroup path in /proc/self/cgroup content has a
// "docker" or "docker-<id>.scope" component.
func cgroupsIndicateDocker(cgroups string) bool {
	for _, line := range strings.Split(cgroups, "\n") {
		path := line
		if idx := strings.LastIndex(line, ":"); idx >= 0 {
			path = line[idx+1:]
		}
		for _, component := range strings.Split(path, "/") {
			if component == "docker" {
				return true
			}
			if id, ok := strings.CutPrefix(component, "docker-"); ok && strings.HasSuffix(id, ".scope") {
				return true
			}
		}
	}
	return false
}

func detectArchitecture() workerpb.EnvironmentInfo_Architecture {
	switch runtime.GOARCH {
	case "amd64":
		return workerpb.EnvironmentInfo_ARCHITECTURE_AMD64
	case "arm64":
		return workerpb.EnvironmentInfo_ARCHITECTURE_ARM64
	default:
		return workerpb.EnvironmentInfo_ARCHITECTURE_UNSPECIFIED
	}
}
