//go:build !linux && !darwin && !windows

package internal

import workerpb "go.temporal.io/api/worker/v1"

func detectPlatform() *workerpb.EnvironmentInfo_Platform {
	return nil
}
