package temporal

import "go.temporal.io/sdk/internal"

// PausePolicy defines when an activity should be automatically paused.
//
// WARNING: Activity pause policy is currently experimental.
type PausePolicy = internal.PausePolicy
