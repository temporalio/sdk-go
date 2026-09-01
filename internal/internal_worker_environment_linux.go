package internal

import (
	"bufio"
	"os"
	"strings"

	workerpb "go.temporal.io/api/worker/v1"
)

func detectPlatform() *workerpb.EnvironmentInfo_Platform {
	return &workerpb.EnvironmentInfo_Platform{
		Variant: &workerpb.EnvironmentInfo_Platform_Linux{
			Linux: &workerpb.EnvironmentInfo_LinuxPlatform{
				Version:      linuxVersion(),
				Architecture: detectArchitecture(),
			},
		},
	}
}

// linuxVersion prefers the distribution version from os-release and falls back to the kernel
// release, matching what Core reports.
func linuxVersion() string {
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if version := osReleaseValue(path, "VERSION_ID"); version != "" {
			return version
		}
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(release))
}

func osReleaseValue(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), "=")
		if ok && k == key {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
