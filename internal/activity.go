// Copyright (c) 2017 Uber Technologies, Inc.
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

	"github.com/uber-go/tally"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"go.uber.org/cadence/internal/common"
	"go.uber.org/zap"
)

type (
	// ActivityType identifies a activity type.
	ActivityType struct {
		Name string
	}

	// ActivityInfo contains information about currently executing activity.
	ActivityInfo struct {
		TaskToken          []byte
		WorkflowExecution  WorkflowExecution
		ActivityID         string
		ActivityType       ActivityType
		TaskList           string
		HeartbeatTimeout   time.Duration // Maximum time between heartbeats. 0 means no heartbeat needed.
		ScheduledTimestamp time.Time     // Time of activity scheduled by a workflow
		StartedTimestamp   time.Time     // Time of activity start
		Deadline           time.Time     // Time of activity timeout
	}

	// RegisterActivityOptions consists of options for registering an activity
	RegisterActivityOptions struct {
		Name string
	}
)

// RegisterActivity - register a activity function with the framework.
// A activity takes a context and input and returns a (result, error) or just error.
// Examples:
//	func sampleActivity(ctx context.Context, input []byte) (result []byte, err error)
//	func sampleActivity(ctx context.Context, arg1 int, arg2 string) (result *customerStruct, err error)
//	func sampleActivity(ctx context.Context) (err error)
//	func sampleActivity() (result string, err error)
//	func sampleActivity(arg1 bool) (result int, err error)
//	func sampleActivity(arg1 bool) (err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
// This method calls panic if activityFunc doesn't comply with the expected format.
func RegisterActivity(activityFunc interface{}) {
	RegisterActivityWithOptions(activityFunc, RegisterActivityOptions{})
}

// RegisterActivityWithOptions registers the activity function with options
// The user can use options to provide an external name for the activity or leave it empty if no
// external name is required. This can be used as
//  client.RegisterActivity(barActivity, RegisterActivityOptions{})
//  client.RegisterActivity(barActivity, RegisterActivityOptions{Name: "barExternal"})
// An activity takes a context and input and returns a (result, error) or just error.
// Examples:
//	func sampleActivity(ctx context.Context, input []byte) (result []byte, err error)
//	func sampleActivity(ctx context.Context, arg1 int, arg2 string) (result *customerStruct, err error)
//	func sampleActivity(ctx context.Context) (err error)
//	func sampleActivity() (result string, err error)
//	func sampleActivity(arg1 bool) (result int, err error)
//	func sampleActivity(arg1 bool) (err error)
// Serialization of all primitive types, structures is supported ... except channels, functions, variadic, unsafe pointer.
// This method calls panic if activityFunc doesn't comply with the expected format.
func RegisterActivityWithOptions(activityFunc interface{}, opts RegisterActivityOptions) {
	thImpl := getHostEnvironment()
	err := thImpl.RegisterActivityWithOptions(activityFunc, opts)
	if err != nil {
		panic(err)
	}
}

// GetActivityInfo returns information about currently executing activity.
func GetActivityInfo(ctx context.Context) ActivityInfo {
	env := getActivityEnv(ctx)
	return ActivityInfo{
		ActivityID:         env.activityID,
		ActivityType:       env.activityType,
		TaskToken:          env.taskToken,
		WorkflowExecution:  env.workflowExecution,
		HeartbeatTimeout:   env.heartbeatTimeout,
		Deadline:           env.deadline,
		ScheduledTimestamp: env.scheduledTimestamp,
		StartedTimestamp:   env.startedTimestamp,
		TaskList:           env.taskList,
	}
}

// GetActivityLogger returns a logger that can be used in activity
func GetActivityLogger(ctx context.Context) *zap.Logger {
	env := getActivityEnv(ctx)
	return env.logger
}

// GetActivityMetricsScope returns a metrics scope that can be used in activity
func GetActivityMetricsScope(ctx context.Context) tally.Scope {
	env := getActivityEnv(ctx)
	return env.metricsScope
}

// RecordActivityHeartbeat sends heartbeat for the currently executing activity
// If the activity is either cancelled (or) workflow/activity doesn't exist then we would cancel
// the context with error context.Canceled.
//  TODO: we don't have a way to distinguish between the two cases when context is cancelled because
//  context doesn't support overriding value of ctx.Error.
//  TODO: Implement automatic heartbeating with cancellation through ctx.
// details - the details that you provided here can be seen in the worflow when it receives TimeoutError, you
// can check error TimeOutType()/Details().
func RecordActivityHeartbeat(ctx context.Context, details ...interface{}) {
	env := getActivityEnv(ctx)
	if env.isLocalActivity {
		// no-op for local activity
		return
	}
	var data []byte
	var err error
	// We would like to be a able to pass in "nil" as part of details(that is no progress to report to)
	if len(details) != 1 || details[0] != nil {
		data, err = encodeArgs(getDataConverterFromActivityCtx(ctx), details)
		if err != nil {
			panic(err)
		}
	}
	err = env.serviceInvoker.Heartbeat(data)
	if err != nil {
		log := GetActivityLogger(ctx)
		log.Debug("RecordActivityHeartbeat With Error:", zap.Error(err))
	}
}

// ServiceInvoker abstracts calls to the Cadence service from an activity implementation.
// Implement to unit test activities.
type ServiceInvoker interface {
	// Returns ActivityTaskCanceledError if activity is cancelled
	Heartbeat(details []byte) error
	Close()
}

// WithActivityTask adds activity specific information into context.
// Use this method to unit test activity implementations that use context extractor methodshared.
func WithActivityTask(
	ctx context.Context,
	task *shared.PollForActivityTaskResponse,
	taskList string,
	invoker ServiceInvoker,
	logger *zap.Logger,
	scope tally.Scope,
	dataConverter encoded.DataConverter,
) context.Context {
	var deadline time.Time
	scheduled := time.Unix(0, task.GetScheduledTimestamp())
	started := time.Unix(0, task.GetStartedTimestamp())
	scheduleToCloseTimeout := time.Duration(task.GetScheduleToCloseTimeoutSeconds()) * time.Second
	startToCloseTimeout := time.Duration(task.GetStartToCloseTimeoutSeconds()) * time.Second
	heartbeatTimeout := time.Duration(task.GetHeartbeatTimeoutSeconds()) * time.Second
	scheduleToCloseDeadline := scheduled.Add(scheduleToCloseTimeout)
	startToCloseDeadline := started.Add(startToCloseTimeout)
	// Minimum of the two deadlines.
	if scheduleToCloseDeadline.Before(startToCloseDeadline) {
		deadline = scheduleToCloseDeadline
	} else {
		deadline = startToCloseDeadline
	}
	return context.WithValue(ctx, activityEnvContextKey, &activityEnvironment{
		taskToken:      task.TaskToken,
		serviceInvoker: invoker,
		activityType:   ActivityType{Name: *task.ActivityType.Name},
		activityID:     *task.ActivityId,
		workflowExecution: WorkflowExecution{
			RunID: *task.WorkflowExecution.RunId,
			ID:    *task.WorkflowExecution.WorkflowId},
		logger:             logger,
		metricsScope:       scope,
		deadline:           deadline,
		heartbeatTimeout:   heartbeatTimeout,
		scheduledTimestamp: scheduled,
		startedTimestamp:   started,
		taskList:           taskList,
		dataConverter:      dataConverter,
	})
}

// ActivityOptions stores all activity-specific parameters that will be stored inside of a context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
type ActivityOptions struct {
	// TaskList that the activity needs to be scheduled on.
	// optional: The default task list with the same name as the workflow task list.
	TaskList string

	// ScheduleToCloseTimeout - The end to end time out for the activity needed.
	// The zero value of this uses default value.
	// Optional: The default value is the sum of ScheduleToStartTimeout and StartToCloseTimeout
	ScheduleToCloseTimeout time.Duration

	// ScheduleToStartTimeout - The queue time out before the activity starts executed.
	// Mandatory: No default.
	ScheduleToStartTimeout time.Duration

	// StartToCloseTimeout - The time out from the start of execution to end of it.
	// Mandatory: No default.
	StartToCloseTimeout time.Duration

	// HeartbeatTimeout - The periodic timeout while the activity is in execution. This is
	// the max interval the server needs to hear at-least one ping from the activity.
	// Optional: Default zero, means no heart beating is needed.
	HeartbeatTimeout time.Duration

	// WaitForCancellation - Whether to wait for cancelled activity to be completed(
	// activity can be failed, completed, cancel accepted)
	// Optional: default false
	WaitForCancellation bool

	// ActivityID - Business level activity ID, this is not needed for most of the cases if you have
	// to specify this then talk to cadence team. This is something will be done in future.
	// Optional: default empty string
	ActivityID string
}

// LocalActivityOptions stores local activity specific parameters that will be stored inside of a context.
type LocalActivityOptions struct {
	// ScheduleToCloseTimeout - The end to end timeout for the local activity.
	// This field is required.
	ScheduleToCloseTimeout time.Duration
}

// WithActivityOptions adds all options to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithActivityOptions(ctx Context, options ActivityOptions) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	eap := getActivityOptions(ctx1)

	eap.TaskListName = options.TaskList
	eap.ScheduleToCloseTimeoutSeconds = common.Int32Ceil(options.ScheduleToCloseTimeout.Seconds())
	eap.StartToCloseTimeoutSeconds = common.Int32Ceil(options.StartToCloseTimeout.Seconds())
	eap.ScheduleToStartTimeoutSeconds = common.Int32Ceil(options.ScheduleToStartTimeout.Seconds())
	eap.HeartbeatTimeoutSeconds = common.Int32Ceil(options.HeartbeatTimeout.Seconds())
	eap.WaitForCancellation = options.WaitForCancellation
	eap.ActivityID = common.StringPtr(options.ActivityID)
	return ctx1
}

// WithLocalActivityOptions adds local activity options to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithLocalActivityOptions(ctx Context, options LocalActivityOptions) Context {
	ctx1 := setLocalActivityParametersIfNotExist(ctx)
	opts := getLocalActivityOptions(ctx1)

	opts.ScheduleToCloseTimeoutSeconds = common.Int32Ceil(options.ScheduleToCloseTimeout.Seconds())
	return ctx1
}

// WithTaskList adds a task list to the copy of the context.
func WithTaskList(ctx Context, name string) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).TaskListName = name
	return ctx1
}

// WithScheduleToCloseTimeout adds a timeout to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithScheduleToCloseTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).ScheduleToCloseTimeoutSeconds = common.Int32Ceil(d.Seconds())
	return ctx1
}

// WithScheduleToStartTimeout adds a timeout to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithScheduleToStartTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).ScheduleToStartTimeoutSeconds = common.Int32Ceil(d.Seconds())
	return ctx1
}

// WithStartToCloseTimeout adds a timeout to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithStartToCloseTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).StartToCloseTimeoutSeconds = common.Int32Ceil(d.Seconds())
	return ctx1
}

// WithHeartbeatTimeout adds a timeout to the copy of the context.
// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
// subjected to change in the future.
func WithHeartbeatTimeout(ctx Context, d time.Duration) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).HeartbeatTimeoutSeconds = common.Int32Ceil(d.Seconds())
	return ctx1
}

// WithWaitForCancellation adds wait for the cacellation to the copy of the context.
func WithWaitForCancellation(ctx Context, wait bool) Context {
	ctx1 := setActivityParametersIfNotExist(ctx)
	getActivityOptions(ctx1).WaitForCancellation = wait
	return ctx1
}
