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

package workflow

import (
	"errors"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

type (

	// ChildWorkflowFuture represents the result of a child workflow execution
	ChildWorkflowFuture = internal.ChildWorkflowFuture

	// Type identifies a workflow type.
	Type = internal.WorkflowType

	// Execution Details.
	Execution = internal.WorkflowExecution

	// Version represents a change version. See GetVersion call.
	Version = internal.Version

	// ChildWorkflowOptions stores all child workflow specific parameters that will be stored inside of a Context.
	ChildWorkflowOptions = internal.ChildWorkflowOptions

	// RegisterOptions consists of options for registering a workflow
	RegisterOptions = internal.RegisterWorkflowOptions

	// Info information about currently executing workflow
	Info = internal.WorkflowInfo

	// UpdateInfo information about a currently running update
	//
	// NOTE: Experimental
	UpdateInfo = internal.UpdateInfo

	// ContinueAsNewError can be returned by a workflow implementation function and indicates that
	// the workflow should continue as new with the same WorkflowID, but new RunID and new history.
	ContinueAsNewError = internal.ContinueAsNewError

	UpdateHandlerOptions = internal.UpdateHandlerOptions
)

// ExecuteActivity requests activity execution in the context of a workflow.
// Context can be used to pass the settings for this activity.
// For example: task queue that this need to be routed, timeouts that need to be configured.
// Use ActivityOptions to pass down the options.
//
//	 ao := ActivityOptions{
//		    TaskQueue: "exampleTaskQueue",
//		    ScheduleToStartTimeout: 10 * time.Second,
//		    StartToCloseTimeout: 5 * time.Second,
//		    ScheduleToCloseTimeout: 10 * time.Second,
//		    HeartbeatTimeout: 0,
//		}
//		ctx := WithActivityOptions(ctx, ao)
//
// Or to override a single option
//
//	ctx := WithTaskQueue(ctx, "exampleTaskQueue")
//
// Input activity is either an activity name (string) or a function representing an activity that is getting scheduled.
// Note that the function implementation is ignored by this call.
// It uses function to extract activity type string from it.
// Input args are the arguments that need to be passed to the scheduled activity.
// To call an activity that is a member of a structure use the function reference with nil receiver.
// For example if an activity is defined as:
//
//	type Activities struct {
//	  ... // members
//	}
//
//	func (a *Activities) Activity1() (string, error) {
//	   ...
//	}
//
// Then a workflow can invoke it as:
//
//	var a *Activities
//	workflow.ExecuteActivity(ctx, a.Activity1)
//
// If the activity failed to complete then the future get error would indicate the failure.
// The error will be of type *ActivityError. It will have important activity information and actual error that caused
// activity failure. Use errors.Unwrap to get this error or errors.As to check it type which can be one of
// *ApplicationError, *TimeoutError, *CanceledError, or *PanicError.
//
// You can cancel the pending activity using context(workflow.WithCancel(ctx)) and that will fail the activity with
// *CanceledError set as cause for *ActivityError. The context in the activity only becomes aware of the cancellation
// when a heartbeat is sent to the server. Since heartbeats may be batched internally, this could take up to the
// HeartbeatTimeout to appear or several minutes by default if that value is not set.
//
// ExecuteActivity immediately returns a Future that can be used to block waiting for activity result or failure.
func ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) Future {
	return internal.ExecuteActivity(ctx, activity, args...)
}

// ExecuteLocalActivity requests to run a local activity. A local activity is like a regular activity with some key
// differences:
//
// • Local activity is scheduled and run by the workflow worker locally.
//
// • Local activity does not need Temporal server to schedule activity task and does not rely on activity worker.
//
// • No need to register local activity.
//
// • Local activity is for short living activities (usually finishes within seconds).
//
// • Local activity cannot heartbeat.
//
// Context can be used to pass the settings for this local activity.
// For now there is only one setting for timeout to be set:
//
//	lao := LocalActivityOptions{
//		ScheduleToCloseTimeout: 5 * time.Second,
//	}
//	ctx := WithLocalActivityOptions(ctx, lao)
//
// The timeout here should be relative shorter than the WorkflowTaskTimeout of the workflow. If you need a
// longer timeout, you probably should not use local activity and instead should use regular activity. Local activity is
// designed to be used for short living activities (usually finishes within seconds).
//
// Input args are the arguments that will to be passed to the local activity. The input args will be hand over directly
// to local activity function without serialization/deserialization because we don't need to pass the input across process
// boundary. However, the result will still go through serialization/deserialization because we need to record the result
// as history to temporal server so if the workflow crashes, a different worker can replay the history without running
// the local activity again.
//
// If the activity failed to complete then the future get error would indicate the failure.
// The error will be of type *ActivityError. It will have important activity information and actual error that caused
// activity failure. Use errors.Unwrap to get this error or errors.As to check it type which can be one of
// *ApplicationError, *TimeoutError, *CanceledError, or *PanicError.
//
// You can cancel the pending activity using context(workflow.WithCancel(ctx)) and that will fail the activity with
// *CanceledError set as cause for *ActivityError.
//
// ExecuteLocalActivity returns Future with local activity result or failure.
func ExecuteLocalActivity(ctx Context, activity interface{}, args ...interface{}) Future {
	return internal.ExecuteLocalActivity(ctx, activity, args...)
}

// ExecuteChildWorkflow requests child workflow execution in the context of a workflow.
// Context can be used to pass the settings for the child workflow.
// For example: task queue that this child workflow should be routed, timeouts that need to be configured.
// Use ChildWorkflowOptions to pass down the options.
//
//	 cwo := ChildWorkflowOptions{
//		    WorkflowExecutionTimeout: 10 * time.Minute,
//		    WorkflowTaskTimeout: time.Minute,
//		}
//	 ctx := WithChildOptions(ctx, cwo)
//
// Input childWorkflow is either a workflow name or a workflow function that is getting scheduled.
// Input args are the arguments that need to be passed to the child workflow function represented by childWorkflow.
//
// If the child workflow failed to complete then the future get error would indicate the failure.
// The error will be of type *ChildWorkflowExecutionError. It will have important child workflow information and actual error that caused
// child workflow failure. Use errors.Unwrap to get this error or errors.As to check it type which can be one of
// *ApplicationError, *TimeoutError, or *CanceledError.
//
// You can cancel the pending child workflow using context(workflow.WithCancel(ctx)) and that will fail the workflow with
// *CanceledError set as cause for *ChildWorkflowExecutionError.
//
// ExecuteChildWorkflow returns ChildWorkflowFuture.
func ExecuteChildWorkflow(ctx Context, childWorkflow interface{}, args ...interface{}) ChildWorkflowFuture {
	return internal.ExecuteChildWorkflow(ctx, childWorkflow, args...)
}

// GetInfo extracts info of a current workflow from a context.
func GetInfo(ctx Context) *Info {
	return internal.GetWorkflowInfo(ctx)
}

func GetUpdateInfo(ctx Context) *UpdateInfo {
	return internal.GetUpdateInfo(ctx)
}

// GetLogger returns a logger to be used in workflow's context
func GetLogger(ctx Context) log.Logger {
	return internal.GetLogger(ctx)
}

// GetMetricsHandler returns a metrics handler to be used in workflow's context.
// This handler does not record metrics during replay.
func GetMetricsHandler(ctx Context) metrics.Handler {
	return internal.GetMetricsHandler(ctx)
}

// GetUnhandledSignalNames returns signal names that have unconsumed signals.
func GetUnhandledSignalNames(ctx Context) []string {
	return internal.GetUnhandledSignalNames(ctx)
}

// RequestCancelExternalWorkflow can be used to request cancellation of an external workflow.
// Input workflowID is the workflow ID of target workflow.
// Input runID indicates the instance of a workflow. Input runID is optional (default is ""). When runID is not specified,
// then the currently running instance of that workflowID will be used.
// By default, the current workflow's namespace will be used as target namespace. However, you can specify a different namespace
// of the target workflow using the context like:
//
//	ctx := WithWorkflowNamespace(ctx, "namespace")
//
// RequestCancelExternalWorkflow return Future with failure or empty success result.
func RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	return internal.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

// SignalExternalWorkflow can be used to send signal info to an external workflow.
// Input workflowID is the workflow ID of target workflow.
// Input runID indicates the instance of a workflow. Input runID is optional (default is ""). When runID is not specified,
// then the currently running instance of that workflowID will be used.
// By default, the current workflow's namespace will be used as target namespace. However, you can specify a different namespace
// of the target workflow using the context like:
//
//	ctx := WithWorkflowNamespace(ctx, "namespace")
//
// SignalExternalWorkflow return Future with failure or empty success result.
func SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg interface{}) Future {
	return internal.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

// GetSignalChannel returns channel corresponding to the signal name.
func GetSignalChannel(ctx Context, signalName string) ReceiveChannel {
	return internal.GetSignalChannel(ctx, signalName)
}

// SideEffect executes the provided function once, records its result into the workflow history. The recorded result on
// history will be returned without executing the provided function during replay. This guarantees the deterministic
// requirement for workflow as the exact same result will be returned in replay.
// Common use case is to run some short non-deterministic code in workflow, like getting random number or new UUID.
// The only way to fail SideEffect is to panic which causes workflow task failure. The workflow task after timeout is
// rescheduled and re-executed giving SideEffect another chance to succeed.
//
// Caution: do not use SideEffect to modify closures. Always retrieve result from SideEffect's encoded return value.
// For example this code is BROKEN:
//
//	// Bad example:
//	var random int
//	workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
//	       random = rand.Intn(100)
//	       return nil
//	})
//	// random will always be 0 in replay, thus this code is non-deterministic
//	if random < 50 {
//	       ....
//	} else {
//	       ....
//	}
//
// On replay the provided function is not executed, the random will always be 0, and the workflow could takes a
// different path breaking the determinism.
//
// Here is the correct way to use SideEffect:
//
//	// Good example:
//	encodedRandom := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
//	      return rand.Intn(100)
//	})
//	var random int
//	encodedRandom.Get(&random)
//	if random < 50 {
//	       ....
//	} else {
//	       ....
//	}
func SideEffect(ctx Context, f func(ctx Context) interface{}) converter.EncodedValue {
	return internal.SideEffect(ctx, f)
}

// MutableSideEffect executes the provided function once, then it looks up the history for the value with the given id.
// If there is no existing value, then it records the function result as a value with the given id on history;
// otherwise, it compares whether the existing value from history has changed from the new function result by calling
// the provided equals function. If they are equal, it returns the value without recording a new one in history;
// otherwise, it records the new value with the same id on history.
//
// Caution: do not use MutableSideEffect to modify closures. Always retrieve result from MutableSideEffect's encoded
// return value.
//
// The difference between MutableSideEffect() and SideEffect() is that every new SideEffect() call in non-replay will
// result in a new marker being recorded on history. However, MutableSideEffect() only records a new marker if the value
// changed. During replay, MutableSideEffect() will not execute the function again, but it will return the exact same
// value as it was returning during the non-replay run.
//
// One good use case of MutableSideEffect() is to access dynamically changing config without breaking determinism.
func MutableSideEffect(ctx Context, id string, f func(ctx Context) interface{}, equals func(a, b interface{}) bool) converter.EncodedValue {
	return internal.MutableSideEffect(ctx, id, f, equals)
}

// DefaultVersion is a version returned by GetVersion for code that wasn't versioned before
const DefaultVersion Version = internal.DefaultVersion

// GetVersion is used to safely perform backwards incompatible changes to workflow definitions.
// It is not allowed to update workflow code while there are workflows running as it is going to break
// determinism. The solution is to have both old code that is used to replay existing workflows
// as well as the new one that is used when it is executed for the first time.
// GetVersion returns maxSupported version when is executed for the first time. This version is recorded into the
// workflow history as a marker event. Even if maxSupported version is changed the version that was recorded is
// returned on replay. DefaultVersion constant contains version of code that wasn't versioned before.
// For example initially workflow has the following code:
//
//	err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
//
// it should be updated to
//
//	err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//
// The backwards compatible way to execute the update is
//
//	v :=  GetVersion(ctx, "fooChange", DefaultVersion, 0)
//	if v  == DefaultVersion {
//	    err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
//	} else {
//	    err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//	}
//
// Then bar has to be changed to baz:
//
//	v :=  GetVersion(ctx, "fooChange", DefaultVersion, 1)
//	if v  == DefaultVersion {
//	    err = workflow.ExecuteActivity(ctx, foo).Get(ctx, nil)
//	} else if v == 0 {
//	    err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//	} else {
//	    err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//	}
//
// Later when there are no workflow executions running DefaultVersion the correspondent branch can be removed:
//
//	v :=  GetVersion(ctx, "fooChange", 0, 1)
//	if v == 0 {
//	    err = workflow.ExecuteActivity(ctx, bar).Get(ctx, nil)
//	} else {
//	    err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//	}
//
// It is recommended to keep the GetVersion() call even if single branch is left:
//
//	GetVersion(ctx, "fooChange", 1, 1)
//	err = workflow.ExecuteActivity(ctx, baz).Get(ctx, nil)
//
// The reason to keep it is: 1) it ensures that if there is older version execution still running, it will fail here
// and not proceed; 2) if you ever need to make more changes for “fooChange”, for example change activity from baz to qux,
// you just need to update the maxVersion from 1 to 2.
//
// Note that, you only need to preserve the first call to GetVersion() for each changeID. All subsequent call to GetVersion()
// with same changeID are safe to remove. However, if you really want to get rid of the first GetVersion() call as well,
// you can do so, but you need to make sure: 1) all older version executions are completed; 2) you can no longer use “fooChange”
// as changeID. If you ever need to make changes to that same part like change from baz to qux, you would need to use a
// different changeID like “fooChange-fix2”, and start minVersion from DefaultVersion again. The code would looks like:
//
//	v := workflow.GetVersion(ctx, "fooChange-fix2", workflow.DefaultVersion, 0)
//	if v == workflow.DefaultVersion {
//	  err = workflow.ExecuteActivity(ctx, baz, data).Get(ctx, nil)
//	} else {
//	  err = workflow.ExecuteActivity(ctx, qux, data).Get(ctx, nil)
//	}
func GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version {
	return internal.GetVersion(ctx, changeID, minSupported, maxSupported)
}

// SetQueryHandler sets the query handler to handle workflow query. The queryType specify which query type this handler
// should handle. The handler must be a function that returns 2 values. The first return value must be a serializable
// result. The second return value must be an error. The handler function could receive any number of input parameters.
// All the input parameter must be serializable. You should call workflow.SetQueryHandler() at the beginning of the workflow
// code. When client calls Client.QueryWorkflow() to temporal server, a task will be generated on server that will be dispatched
// to a workflow worker, which will replay the history events and then execute a query handler based on the query type.
// The query handler will be invoked out of the context of the workflow, meaning that the handler code must not use workflow
// context to do things like [workflow.NewChannel](), [workflow.Go]() or to call any workflow blocking functions like
// Channel.Get() or Future.Get(). Trying to do so in query handler code will fail the query and client will receive
// QueryFailedError.
// Example of workflow code that support query type "current_state":
//
//	func MyWorkflow(ctx workflow.Context, input string) error {
//	  currentState := "started" // this could be any serializable struct
//	  err := workflow.SetQueryHandler(ctx, "current_state", func() (string, error) {
//	    return currentState, nil
//	  })
//	  if err != nil {
//	    currentState = "failed to register query handler"
//	    return err
//	  }
//	  // your normal workflow code begins here, and you update the currentState as the code makes progress.
//	  currentState = "waiting timer"
//	  err = NewTimer(ctx, time.Hour).Get(ctx, nil)
//	  if err != nil {
//	    currentState = "timer failed"
//	    return err
//	  }
//
//	  currentState = "waiting activity"
//	  ctx = WithActivityOptions(ctx, myActivityOptions)
//	  err = ExecuteActivity(ctx, MyActivity, "my_input").Get(ctx, nil)
//	  if err != nil {
//	    currentState = "activity failed"
//	    return err
//	  }
//	  currentState = "done"
//	  return nil
//	}
func SetQueryHandler(ctx Context, queryType string, handler interface{}) error {
	return internal.SetQueryHandler(ctx, queryType, handler)
}

// SetUpdateHandler forwards to SetUpdateHandlerWithOptions with an
// zero-initialized UpdateHandlerOptions struct. See SetUpdateHandlerWithOptions
// for more details.
//
// NOTE: Experimental
func SetUpdateHandler(ctx Context, updateName string, handler interface{}) error {
	return SetUpdateHandlerWithOptions(ctx, updateName, handler, UpdateHandlerOptions{})
}

// SetUpdateHandlerWithOptions binds an update handler function to the specified
// name such that update invocations specifying that name will invoke the
// handler.  The handler function can take as input any number of parameters so
// long as they can be serialized/deserialized by the system. The handler can
// take a [workflow.Context] as its first parameter but this is not required. The
// update handler must return either a single error or a single serializable
// object along with a single error. The update handler function is invoked in
// the context of the workflow and thus is subject to the same restrictions as
// workflow code, namely, the update handler must be deterministic. As with
// other workflow code, update code is free to invoke and wait on the results of
// activities. Update handler code is free to mutate workflow state.
//
// This registration can optionally specify (through UpdateHandlerOptions) an
// update validation function. If provided, this function will be invoked before
// the update handler itself is invoked and if this function returns an error,
// the update request will be considered to have been rejected and as such will
// not occupy any space in the workflow history. Validation functions must take
// as inputs the same parameters as the associated update handler but my vary
// from said handler by the presence/absence of a [workflow.Context] as the first
// parameter. Validation handlers must only return a single error. Validation
// handlers must be deterministic and can observe workflow state but must not
// mutate workflow state in any way.
//
// Example of workflow code that supports a monotonic counter
//
//	func MyWorkflow(ctx workflow.Context) (int, error) {
//		counter := 0
//		err := workflow.SetUpdateHandlerWithOptions(
//			ctx,
//			"add",
//			func(val int) (int, error) { // Calls
//				counter += val // note that this mutates workflow state
//				return counter, nil
//			},
//			UpdateHandlerOptions{
//				Validator: func(val int) error {
//					if val < 0 { // reject attempts to add negative values
//						return fmt.Errorf("invalid addend: %v", val)
//					}
//					return nil
//				},
//			})
//		if err != nil {
//			return 0, err
//		}
//		_ = ctx.Done().Receive(ctx, nil)
//		return counter, nil
//	}
//
// NOTE: Experimental
func SetUpdateHandlerWithOptions(ctx Context, updateName string, handler interface{}, opts UpdateHandlerOptions) error {
	return internal.SetUpdateHandler(ctx, updateName, handler, opts)
}

// IsReplaying returns whether the current workflow code is replaying.
//
// Warning! Never make commands, like schedule activity/childWorkflow/timer or send/wait on future/channel, based on
// this flag as it is going to break workflow determinism requirement.
// The only reasonable use case for this flag is to avoid some external actions during replay, like custom logging or
// metric reporting. Please note that Temporal already provide standard logging/metric via [workflow.GetLogger] and
// [workflow.GetMetricsHandler], and those standard mechanism are replay-aware and it will automatically suppress
// during replay. Only use this flag if you need custom logging/metrics reporting, for example if you want to log to
// kafka.
//
// Warning! Any action protected by this flag should not fail or if it does fail should ignore that failure or panic
// on the failure. If workflow don't want to be blocked on those failure, it should ignore those failure; if workflow do
// want to make sure it proceed only when that action succeed then it should panic on that failure. Panic raised from a
// workflow causes workflow task to fail and temporal server will rescheduled later to retry.
func IsReplaying(ctx Context) bool {
	return internal.IsReplaying(ctx)
}

// HasLastCompletionResult checks if there is completion result from previous runs.
// This is used in combination with cron schedule. A workflow can be started with an optional cron schedule.
// If a cron workflow wants to pass some data to next schedule, it can return any data and that data will become
// available when next run starts.
// This HasLastCompletionResult() checks if there is such data available passing down from previous successful run.
func HasLastCompletionResult(ctx Context) bool {
	return internal.HasLastCompletionResult(ctx)
}

// GetLastCompletionResult extract last completion result from the last successful run for this cron or schedule workflow.
// This is used in combination with cron schedule or schedule workflow. A workflow can be started with an optional cron schedule.
// If a cron workflow wants to pass some data to next schedule, it can return any data and that data will become
// available when next run starts. This will contain the last successful result even if the most recent run failed.
// This GetLastCompletionResult() extract the data into expected data structure.
// See TestWorkflowEnvironment.SetLastCompletionResult() for unit test support.
//
// Note, values should not be reused for extraction here because merging on top
// of existing values may result in unexpected behavior similar to
// json.Unmarshal.
func GetLastCompletionResult(ctx Context, d ...interface{}) error {
	return internal.GetLastCompletionResult(ctx, d...)
}

// GetLastError extracts the error from the last run of this workflow. If the last run of this workflow did not fail or
// this is the first run, this will be nil. This is used in combination with cron schedule or schedule workflow.
//
// See TestWorkflowEnvironment.SetLastError() for unit test support.
func GetLastError(ctx Context) error {
	return internal.GetLastError(ctx)
}

// UpsertSearchAttributes is used to add or update workflow search attributes.
// The search attributes can be used in query of List/Scan/Count workflow APIs.
// The key and value type must be registered on temporal server side;
// The value has to deterministic when replay;
// The value has to be Json serializable.
// UpsertSearchAttributes will merge attributes to existing map in workflow, for example workflow code:
//
//	  func MyWorkflow(ctx workflow.Context, input string) error {
//		   attr1 := map[string]interface{}{
//			   "CustomIntField": 1,
//			   "CustomBoolField": true,
//		   }
//		   workflow.UpsertSearchAttributes(ctx, attr1)
//
//		   attr2 := map[string]interface{}{
//			   "CustomIntField": 2,
//			   "CustomKeywordField": "seattle",
//		   }
//		   workflow.UpsertSearchAttributes(ctx, attr2)
//	  }
//
// will eventually have search attributes:
//
//	map[string]interface{}{
//		"CustomIntField": 2,
//		"CustomBoolField": true,
//		"CustomKeywordField": "seattle",
//	}
//
// For supported operations on different server versions see [Visibility].
//
// [Visibility]: https://docs.temporal.io/visibility
func UpsertSearchAttributes(ctx Context, attributes map[string]interface{}) error {
	return internal.UpsertSearchAttributes(ctx, attributes)
}

// UpsertMemo is used to add or update workflow memo.
// UpsertMemo will merge keys to the existing map in workflow. For example:
//
//	func MyWorkflow(ctx workflow.Context, input string) error {
//		memo1 := map[string]interface{}{
//			"Key1": 1,
//			"Key2": true,
//		}
//		workflow.UpsertMemo(ctx, memo1)
//
//		memo2 := map[string]interface{}{
//			"Key1": 2,
//			"Key3": "seattle",
//		}
//		workflow.UpsertMemo(ctx, memo2)
//	}
//
// The workflow memo will eventually be:
//
//	map[string]interface{}{
//		"Key1": 2,
//		"Key2": true,
//		"Key3": "seattle",
//	}
//
// This is only supported with Temporal Server 1.18+
func UpsertMemo(ctx Context, memo map[string]interface{}) error {
	return internal.UpsertMemo(ctx, memo)
}

// NewContinueAsNewError creates ContinueAsNewError instance
// If the workflow main function returns this error then the current execution is ended and
// the new execution with same workflow ID is started automatically with options
// provided to this function.
//
//	 ctx - use context to override any options for the new workflow like execution timeout, workflow task timeout, task queue.
//		  if not mentioned it would use the defaults that the current workflow is using.
//	       ctx := WithWorkflowExecutionTimeout(ctx, 30 * time.Minute)
//	       ctx := WithWorkflowTaskTimeout(ctx, time.Minute)
//		  ctx := WithWorkflowTaskQueue(ctx, "example-group")
//	 wfn - workflow function. for new execution it can be different from the currently running.
//	 args - arguments for the new workflow.
func NewContinueAsNewError(ctx Context, wfn interface{}, args ...interface{}) error {
	return internal.NewContinueAsNewError(ctx, wfn, args...)
}

// IsContinueAsNewError return if the err is a ContinueAsNewError
func IsContinueAsNewError(err error) bool {
	var continueAsNewErr *ContinueAsNewError
	return errors.As(err, &continueAsNewErr)
}

// DataConverterWithoutDeadlockDetection returns a data converter that disables
// workflow deadlock detection for each call on the data converter. This should
// be used for advanced data converters that may perform remote calls or
// otherwise intentionally execute longer than the default deadlock detection
// timeout.
func DataConverterWithoutDeadlockDetection(c converter.DataConverter) converter.DataConverter {
	return internal.DataConverterWithoutDeadlockDetection(c)
}
