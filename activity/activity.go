// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package activity

import (
	"context"

	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

type (
	// Type identifies an activity type.
	Type = internal.ActivityType

	// Info contains information about a currently executing activity.
	Info = internal.ActivityInfo

	// RegisterOptions consists of options for registering an activity
	RegisterOptions = internal.RegisterActivityOptions
)

// ErrResultPending is returned from activity's implementation to indicate the activity is not completed when
// activity method returns. Activity needs to be completed by Client.CompleteActivity() separately. For example, if an
// activity require human interaction (like approve an expense report), the activity could return ErrResultPending
// which indicate the activity is not done yet. Then, when the waited human action happened, it needs to trigger something
// that could report the activity completed event to temporal server via Client.CompleteActivity() API.
var ErrResultPending = internal.ErrActivityResultPending

// GetInfo returns information about currently executing activity.
func GetInfo(ctx context.Context) Info {
	return internal.GetActivityInfo(ctx)
}

// GetLogger returns a logger that can be used in activity
func GetLogger(ctx context.Context) log.Logger {
	return internal.GetActivityLogger(ctx)
}

// GetMetricsHandler returns a metrics handler that can be used in activity
func GetMetricsHandler(ctx context.Context) metrics.Handler {
	return internal.GetActivityMetricsHandler(ctx)
}

// RecordHeartbeat sends heartbeat for the currently executing activity
// If the activity is either canceled (or) workflow/activity doesn't exist then we would cancel
// the context with error context.Canceled.
//
// details - the details that you provided here can be seen in the workflow when it receives TimeoutError, you
// can check error with TimeoutType()/Details().
func RecordHeartbeat(ctx context.Context, details ...interface{}) {
	internal.RecordActivityHeartbeat(ctx, details...)
}

// HasHeartbeatDetails checks if there is heartbeat details from last attempt.
func HasHeartbeatDetails(ctx context.Context) bool {
	return internal.HasHeartbeatDetails(ctx)
}

// GetHeartbeatDetails extract heartbeat details from last failed attempt. This is used in combination with retry policy.
// An activity could be scheduled with an optional retry policy on ActivityOptions. If the activity failed then server
// would attempt to dispatch another activity task to retry according to the retry policy. If there was heartbeat
// details reported by activity from the failed attempt, the details would be delivered along with the activity task for
// retry attempt. Activity could extract the details by GetHeartbeatDetails() and resume from the progress.
// See TestActivityEnvironment.SetHeartbeatDetails() for unit test support.
func GetHeartbeatDetails(ctx context.Context, d ...interface{}) error {
	return internal.GetHeartbeatDetails(ctx, d...)
}

// GetWorkerStopChannel returns a read-only channel. The closure of this channel indicates the activity worker is stopping.
// When the worker is stopping, it will close this channel and wait until the worker stop timeout finishes. After the timeout
// hit, the worker will cancel the activity context and then exit. The timeout can be defined by worker option: WorkerStopTimeout.
// Use this channel to handle activity graceful exit when the activity worker stops.
func GetWorkerStopChannel(ctx context.Context) <-chan struct{} {
	return internal.GetWorkerStopChannel(ctx)
}
