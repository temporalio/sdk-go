package internal

import (
	"fmt"

	workerpb "go.temporal.io/api/worker/v1"
	"golang.org/x/sys/windows"
)

func detectPlatform() *workerpb.EnvironmentInfo_Platform {
	info := windows.RtlGetVersion()
	return &workerpb.EnvironmentInfo_Platform{
		Variant: &workerpb.EnvironmentInfo_Platform_Windows{
			Windows: &workerpb.EnvironmentInfo_WindowsPlatform{
				Version:      fmt.Sprintf("%d.%d.%d", info.MajorVersion, info.MinorVersion, info.BuildNumber),
				Architecture: detectArchitecture(),
				// Go binaries do not link against a C runtime.
				Crt: workerpb.EnvironmentInfo_WindowsPlatform_CRT_UNSPECIFIED,
			},
		},
	}
}
