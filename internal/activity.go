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

package internal

import (
	"context"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/uber-go/tally"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/common"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

type (
	// ActivityType identifies a activity type.
	ActivityType struct {
		Name string
	}

	// ActivityInfo contains information about currently executing activity.
	ActivityInfo struct {
		TaskToken         []byte
		WorkflowType      *WorkflowType
		WorkflowNamespace string
		WorkflowExecution WorkflowExecution
		ActivityID        string
		ActivityType      ActivityType
		TaskQueue         string
		HeartbeatTimeout  time.Duration // Maximum time between heartbeats. 0 means no heartbeat needed.
		ScheduledTime     time.Time     // Time of activity scheduled by a workflow
		StartedTime       time.Time     // Time of activity start
		Deadline          time.Time     // Time of activity timeout
		Attempt           int32         // Attempt starts from 1, and increased by 1 for every retry if retry policy is specified.
	}

	// RegisterActivityOptions consists of options for registering an activity
	RegisterActivityOptions struct {
		// When an activity is a function the name is an actual activity type name.
		// When an activity is part of a structure then each member of the structure becomes an activity with
		// this Name as a prefix + activity function name.
		Name                          string
		DisableAlreadyRegisteredCheck bool
	}

	// ActivityOptions stores all activity-specific parameters that will be stored inside of a context.
	// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
	// subjected to change in the future.
	ActivityOptions struct {
		// TaskQueue that the activity needs to be scheduled on.
		// optional: The default task queue with the same name as the workflow task queue.
		TaskQueue string

		// ScheduleToCloseTimeout - The end to end timeout for the activity needed.
		// The zero value of this uses default value.
		// Optional: The default value is the sum of ScheduleToStartTimeout and StartToCloseTimeout
		ScheduleToCloseTimeout time.Duration

		// ScheduleToStartTimeout - The queue timeout before the activity starts executed.
		// Mandatory: No default.
		ScheduleToStartTimeout time.Duration

		// StartToCloseTimeout - The timeout from the start of execution to end of it.
		// Mandatory: No default.
		StartToCloseTimeout time.Duration

		// HeartbeatTimeout - The periodic timeout while the activity is in execution. This is
		// the max interval the server needs to hear at-least one ping from the activity.
		// Optional: Default zero, means no heart beating is needed.
		HeartbeatTimeout time.Duration

		// WaitForCancellation - Whether to wait for canceled activity to be completed(
		// activity can be failed, completed, cancel accepted)
		// Optional: default false
		WaitForCancellation bool

		// ActivityID - Business level activity ID, this is not needed for most of the cases if you have
		// to specify this then talk to temporal team. This is something will be done in future.
		// Optional: default empty string
		ActivityID string

		// RetryPolicy specifies how to retry an Activity if an error occurs.
		// More details are available at docs.temporal.io.
		// RetryPolicy is optional. If one is not specified a default RetryPolicy is provided by the server.
		// The default RetryPolicy provided by the server specifies:
		// - InitialInterval of 1 second
		// - BackoffCoefficient of 2.0
		// - MaximumInterval of 100 x InitialInterval
		// - MaximumAttempts of 0 (unlimited)
		// To disable retries set MaximumAttempts to 1.
		// The default RetryPolicy provided by the server can be overridden by the dynamic config.
		RetryPolicy *RetryPolicy
	}

	// LocalActivityOptions stores local activity specific parameters that will be stored inside of a context.
	LocalActivityOptions struct {
		// ScheduleToCloseTimeout - The end to end timeout for the local activity including retries.
		// This field is required.
		ScheduleToCloseTimeout time.Duration

		// StartToCloseTimeout - The timeout for a single execution of the local activity.
		// Optional: defaults to ScheduleToClose
		StartToCloseTimeout time.Duration

		// RetryPolicy specify how to retry activity if error happens.
		// Optional: default is to retry according to the default retry policy up to ScheduleToCloseTimeout
		RetryPolicy *RetryPolicy
	}
)

// GetActivityInfo returns information about currently executing activity.
func GetActivityInfo(ctx context.Context) ActivityInfo {
	env := getActivityEnv(ctx)
	return ActivityInfo{
		ActivityID:        env.activityID,
		ActivityType:      env.activityType,
		TaskToken:         env.taskToken,
		WorkflowExecution: env.workflowExecution,
		HeartbeatTimeout:  env.heartbeatTimeout,
		Deadline:          env.deadline,
		ScheduledTime:     env.scheduledTime,
		StartedTime:       env.startedTime,
		TaskQueue:         env.taskQueue,
		Attempt:           env.attempt,
		WorkflowType:      env.workflowType,
		WorkflowNamespace: env.workflowNamespace,
	}
}

// HasHeartbeatDetails checks if there is heartbeat details from last attempt.
func HasHeartbeatDetails(ctx context.Context) bool {
	env := getActivityEnv(ctx)
	return env.heartbeatDetails != nil
}

// GetHeartbeatDetails extract heartbeat details from last failed attempt. This is used in combination with retry policy.
// An activity could be scheduled with an optional retry policy on ActivityOptions. If the activity failed then server
// would attempt to dispatch another activity task to retry according to the retry policy. If there was heartbeat
// details reported by activity from the failed attempt, the details would be delivered along with the activity task for
// retry attempt. Activity could extract the details by GetHeartbeatDetails() and resume from the progress.
func GetHeartbeatDetails(ctx context.Context, d ...interface{}) error {
	env := getActivityEnv(ctx)
	if env.heartbeatDetails == nil {
		return ErrNoData
	}
	encoded := newEncodedValues(env.heartbeatDetails, env.dataConverter)
	return encoded.Get(d...)
}

// GetActivityLogger returns a logger that can be used in activity
func GetActivityLogger(ctx context.Context) log.Logger {
	env := getActivityEnv(ctx)
	return env.logger
}

// GetActivityMetricsScope returns a metrics scope that can be used in activity
func GetActivityMetricsScope(ctx context.Context) tally.Scope {
	env := getActivityEnv(ctx)
	return env.metricsScope
}

// GetWorkerStopChannel returns a read-only channel. The closure of this channel indicates the activity worker is stopping.
// When the worker is stopping, it will close this channel and wait until the worker stop timeout finishes. After the timeout
// hit, the worker will cancel the activity context and then exit. The timeout can be defined by worker option: WorkerStopTimeout.
// Use this channel to handle activity graceful exit when the activity worker stops.
func GetWorkerStopChannel(ctx context.Context) <-chan struct{} {
	env := getActivityEnv(ctx)
	return env.workerStopChannel
}

// RecordActivityHeartbeat sends heartbeat for the currently executing activity
// If the activity is either canceled (or) workflow/activity doesn't exist then we would cancel
// the context with error context.Canceled.
//  TODO: we don't have a way to distinguish between the two cases when context is canceled because
//  context doesn't support overriding value of ctx.Error.
//  TODO: Implement automatic heartbeating with cancellation through ctx.
// details - the details that you provided here can be seen in the worflow when it receives TimeoutError, you
// can check error TimeoutType()/Details().
func RecordActivityHeartbeat(ctx context.Context, details ...interface{}) {
	env := getActivityEnv(ctx)
	if env.isLocalActivity {
		// no-op for local activity
		return
	}
	var data *commonpb.Payloads
	var err error
	// We would like to be a able to pass in "nil" as part of details(that is no progress to report to)
	if len(details) > 1 || (len(details) == 1 && details[0] != nil) {
		data, err = encodeArgs(getDataConverterFromActivityCtx(ctx), details)
		if err != nil {
			panic(err)
		}
	}

	err = env.serviceInvoker.Heartbeat(ctx, data, false)
	if err != nil {
		log := GetActivityLogger(ctx)
		log.Debug("RecordActivityHeartbeat with error", tagError, err)
	}
}

// ServiceInvoker abstracts calls to the Temporal service from an activity implementation.
// Implement to unit test activities.
type ServiceInvoker interface {
	// Returns ActivityTaskCanceledError if activity is canceled
	Heartbeat(ctx context.Context, details *commonpb.Payloads, skipBatching bool) error
	Close(ctx context.Context, flushBufferedHeartbeat bool)
	GetClient(options ClientOptions) Client
}

// WithActivityTask adds activity specific information into context.
// Use this method to unit test activity implementations that use context extractor methodshared.
func WithActivityTask(
	ctx context.Context,
	task *workflowservice.PollActivityTaskQueueResponse,
	taskQueue string,
	invoker ServiceInvoker,
	logger log.Logger,
	scope tally.Scope,
	dataConverter converter.DataConverter,
	workerStopChannel <-chan struct{},
	contextPropagators []ContextPropagator,
	tracer opentracing.Tracer,
) context.Context {
	var deadline time.Time
	scheduled := common.TimeValue(task.GetScheduledTime())
	started := common.TimeValue(task.GetStartedTime())
	scheduleToCloseTimeout := common.DurationValue(task.GetScheduleToCloseTimeout())
	startToCloseTimeout := common.DurationValue(task.GetStartToCloseTimeout())
	heartbeatTimeout := common.DurationValue(task.GetHeartbeatTimeout())

	startToCloseDeadline := started.Add(startToCloseTimeout)
	if scheduleToCloseTimeout > 0 {
		scheduleToCloseDeadline := scheduled.Add(scheduleToCloseTimeout)
		// Minimum of the two deadlines.
		if scheduleToCloseDeadline.Before(startToCloseDeadline) {
			deadline = scheduleToCloseDeadline
		} else {
			deadline = startToCloseDeadline
		}
	} else {
		deadline = startToCloseDeadline
	}

	logger = ilog.With(logger,
		tagActivityID, task.ActivityId,
		tagActivityType, task.ActivityType.Name,
		tagAttempt, task.Attempt,
		tagWorkflowType, task.WorkflowType.Name,
		tagWorkflowID, task.WorkflowExecution.WorkflowId,
		tagRunID, task.WorkflowExecution.RunId,
	)

	return context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		taskToken:      task.TaskToken,
		serviceInvoker: invoker,
		activityType:   ActivityType{Name: task.ActivityType.Name},
		activityID:     task.ActivityId,
		workflowExecution: WorkflowExecution{
			RunID: task.WorkflowExecution.RunId,
			ID:    task.WorkflowExecution.WorkflowId},
		logger:           logger,
		metricsScope:     scope,
		deadline:         deadline,
		heartbeatTimeout: heartbeatTimeout,
		scheduledTime:    scheduled,
		startedTime:      started,
		taskQueue:        taskQueue,
		dataConverter:    dataConverter,
		attempt:          task.GetAttempt(),
		heartbeatDetails: task.HeartbeatDetails,
		workflowType: &WorkflowType{
			Name: task.WorkflowType.Name,
		},
		workflowNamespace:  task.WorkflowNamespace,
		workerStopChannel:  workerStopChannel,
		contextPropagators: contextPropagators,
		tracer:             tracer,
	})
}
