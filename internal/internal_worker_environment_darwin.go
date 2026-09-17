package internal

import (
	"syscall"

	workerpb "go.temporal.io/api/worker/v1"
)

func detectPlatform() *workerpb.EnvironmentInfo_Platform {
	// kern.osproductversion holds the marketing version (e.g. "14.2"); kern.osrelease is the
	// Darwin kernel version and is only used as a fallback.
	version, err := syscall.Sysctl("kern.osproductversion")
	if err != nil || version == "" {
		version, _ = syscall.Sysctl("kern.osrelease")
	}
	return &workerpb.EnvironmentInfo_Platform{
		Variant: &workerpb.EnvironmentInfo_Platform_Macos{
			Macos: &workerpb.EnvironmentInfo_MacOSPlatform{
				Version:      version,
				Architecture: detectArchitecture(),
			},
		},
	}
}
