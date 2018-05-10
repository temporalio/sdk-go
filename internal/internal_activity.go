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

// All code in this file is private to the package.

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/uber-go/tally"
	"go.uber.org/zap"
	"time"
)

type (
	// activity is an interface of an activity implementation.
	activity interface {
		Execute(ctx context.Context, input []byte) ([]byte, error)
		ActivityType() ActivityType
		GetFunction() interface{}
	}

	activityInfo struct {
		activityID string
	}

	localActivityInfo struct {
		activityID string
	}

	// activityOptions configuration parameters for scheduling an activity
	activityOptions struct {
		ActivityID                    *string // Users can choose IDs but our framework makes it optional to decrease the crust.
		TaskListName                  string
		ScheduleToCloseTimeoutSeconds int32
		ScheduleToStartTimeoutSeconds int32
		StartToCloseTimeoutSeconds    int32
		HeartbeatTimeoutSeconds       int32
		WaitForCancellation           bool
		OriginalTaskListName          string
	}

	localActivityOptions struct {
		ScheduleToCloseTimeoutSeconds int32
	}

	executeActivityParams struct {
		activityOptions
		ActivityType ActivityType
		Input        []byte
	}

	executeLocalActivityParams struct {
		localActivityOptions
		ActivityFn   interface{} // local activity function pointer
		InputArgs    []interface{}
		WorkflowInfo *WorkflowInfo
	}

	// asyncActivityClient for requesting activity execution
	asyncActivityClient interface {
		// The ExecuteActivity schedules an activity with a callback handler.
		// If the activity failed to complete the callback error would indicate the failure
		// and it can be one of ActivityTaskFailedError, ActivityTaskTimeoutError, ActivityTaskCanceledError
		ExecuteActivity(parameters executeActivityParams, callback resultHandler) *activityInfo

		// This only initiates cancel request for activity. if the activity is configured to not waitForCancellation then
		// it would invoke the callback handler immediately with error code ActivityTaskCanceledError.
		// If the activity is not running(either scheduled or started) then it is a no-operation.
		RequestCancelActivity(activityID string)
	}

	// localActivityClient for requesting local activity execution
	localActivityClient interface {
		ExecuteLocalActivity(params executeLocalActivityParams, callback resultHandler) *localActivityInfo

		RequestCancelLocalActivity(activityID string)
	}

	activityEnvironment struct {
		taskToken          []byte
		workflowExecution  WorkflowExecution
		activityID         string
		activityType       ActivityType
		serviceInvoker     ServiceInvoker
		logger             *zap.Logger
		metricsScope       tally.Scope
		isLocalActivity    bool
		heartbeatTimeout   time.Duration
		deadline           time.Time
		scheduledTimestamp time.Time
		startedTimestamp   time.Time
		taskList           string
	}

	// context.WithValue need this type instead of basic type string to avoid lint error
	contextKey string
)

const (
	activityEnvContextKey          contextKey = "activityEnv"
	activityOptionsContextKey      contextKey = "activityOptions"
	localActivityOptionsContextKey contextKey = "localActivityOptions"
)

func getActivityEnv(ctx context.Context) *activityEnvironment {
	env := ctx.Value(activityEnvContextKey)
	if env == nil {
		panic("getActivityEnv: Not an activity context")
	}
	return env.(*activityEnvironment)
}

func getActivityOptions(ctx Context) *activityOptions {
	eap := ctx.Value(activityOptionsContextKey)
	if eap == nil {
		return nil
	}
	return eap.(*activityOptions)
}

func getLocalActivityOptions(ctx Context) *localActivityOptions {
	opts := ctx.Value(localActivityOptionsContextKey)
	if opts == nil {
		return nil
	}
	return opts.(*localActivityOptions)
}

func getValidatedActivityOptions(ctx Context) (*activityOptions, error) {
	p := getActivityOptions(ctx)
	if p == nil {
		// We need task list as a compulsory parameter. This can be removed after registration
		return nil, errActivityParamsBadRequest
	}
	if p.TaskListName == "" {
		// We default to origin task list name.
		p.TaskListName = p.OriginalTaskListName
	}
	if p.ScheduleToStartTimeoutSeconds <= 0 {
		return nil, errors.New("missing or negative ScheduleToStartTimeoutSeconds")
	}
	if p.StartToCloseTimeoutSeconds <= 0 {
		return nil, errors.New("missing or negative StartToCloseTimeoutSeconds")
	}
	if p.ScheduleToCloseTimeoutSeconds < 0 {
		return nil, errors.New("missing or negative ScheduleToCloseTimeoutSeconds")
	}
	if p.ScheduleToCloseTimeoutSeconds == 0 {
		// This is a optional parameter, we default to sum of the other two timeouts.
		p.ScheduleToCloseTimeoutSeconds = p.ScheduleToStartTimeoutSeconds + p.StartToCloseTimeoutSeconds
	}
	if p.HeartbeatTimeoutSeconds < 0 {
		return nil, errors.New("invalid negative HeartbeatTimeoutSeconds")
	}

	return p, nil
}

func getValidatedLocalActivityOptions(ctx Context) (*localActivityOptions, error) {
	p := getLocalActivityOptions(ctx)
	if p == nil {
		return nil, errLocalActivityParamsBadRequest
	}
	if p.ScheduleToCloseTimeoutSeconds <= 0 {
		return nil, errors.New("missing or negative ScheduleToCloseTimeoutSeconds")
	}

	return p, nil
}

func validateFunctionArgs(f interface{}, args []interface{}, isWorkflow bool) error {
	fType := reflect.TypeOf(f)
	if fType == nil || fType.Kind() != reflect.Func {
		return fmt.Errorf("Provided type: %v is not a function type", f)
	}
	fnName := getFunctionName(f)

	fnArgIndex := 0
	// Skip Context function argument.
	if fType.NumIn() > 0 {
		if isWorkflow && isWorkflowContext(fType.In(0)) {
			fnArgIndex++
		}
		if !isWorkflow && isActivityContext(fType.In(0)) {
			fnArgIndex++
		}
	}

	// Validate provided args match with function order match.
	if fType.NumIn()-fnArgIndex != len(args) {
		return fmt.Errorf(
			"expected %d args for function: %v but found %v",
			fType.NumIn()-fnArgIndex, fnName, len(args))
	}

	for i := 0; fnArgIndex < fType.NumIn(); fnArgIndex, i = fnArgIndex+1, i+1 {
		fnArgType := fType.In(fnArgIndex)
		argType := reflect.TypeOf(args[i])
		if argType != nil && !argType.AssignableTo(fnArgType) {
			return fmt.Errorf(
				"cannot assign function argument: %d from type: %s to type: %s",
				fnArgIndex+1, argType, fnArgType,
			)
		}
	}

	return nil
}

func getValidatedActivityFunction(f interface{}, args []interface{}) (*ActivityType, []byte, error) {
	fnName := ""
	fType := reflect.TypeOf(f)
	switch getKind(fType) {
	case reflect.String:
		fnName = reflect.ValueOf(f).String()

	case reflect.Func:
		if err := validateFunctionArgs(f, args, false); err != nil {
			return nil, nil, err
		}
		fnName = getFunctionName(f)
		if alias, ok := getHostEnvironment().getActivityAlias(fnName); ok {
			fnName = alias
		}

	default:
		return nil, nil, fmt.Errorf(
			"Invalid type 'f' parameter provided, it can be either activity function or name of the activity: %v", f)
	}

	input, err := getHostEnvironment().encodeArgs(args)
	if err != nil {
		return nil, nil, err
	}
	return &ActivityType{Name: fnName}, input, nil
}

func getKind(fType reflect.Type) reflect.Kind {
	if fType == nil {
		return reflect.Invalid
	}
	return fType.Kind()
}

func isActivityContext(inType reflect.Type) bool {
	contextElem := reflect.TypeOf((*context.Context)(nil)).Elem()
	return inType != nil && inType.Implements(contextElem)
}

func validateFunctionAndGetResults(f interface{}, values []reflect.Value) ([]byte, error) {
	fnName := getFunctionName(f)
	resultSize := len(values)

	if resultSize < 1 || resultSize > 2 {
		return nil, fmt.Errorf(
			"The function: %v signature returns %d results, it is expecting to return either error or (result, error)",
			fnName, resultSize)
	}

	var result []byte
	var err error

	// Parse result
	if resultSize > 1 {
		retValue := values[0]
		if retValue.Kind() != reflect.Ptr || !retValue.IsNil() {
			result, err = getHostEnvironment().encodeArg(retValue.Interface())
			if err != nil {
				return nil, err
			}
		}
	}

	// Parse error.
	errValue := values[resultSize-1]
	if errValue.IsNil() {
		return result, nil
	}
	errInterface, ok := errValue.Interface().(error)
	if !ok {
		return nil, fmt.Errorf(
			"Failed to parse error result as it is not of error interface: %v",
			errValue)
	}
	return result, errInterface
}

func deSerializeFnResultFromFnType(fnType reflect.Type, result []byte, to interface{}) error {
	if fnType.Kind() != reflect.Func {
		return fmt.Errorf("expecting only function type but got type: %v", fnType)
	}

	// We already validated during registration that it either have (result, error) (or) just error.
	if fnType.NumOut() <= 1 {
		return nil
	} else if fnType.NumOut() == 2 {
		if result == nil {
			return nil
		}
		err := getHostEnvironment().decodeArg(result, to)
		if err != nil {
			return err
		}
	}
	return nil
}

func deSerializeFunctionResult(f interface{}, result []byte, to interface{}) error {
	fType := reflect.TypeOf(f)

	switch getKind(fType) {
	case reflect.Func:
		// We already validated that it either have (result, error) (or) just error.
		return deSerializeFnResultFromFnType(fType, result, to)

	case reflect.String:
		// If we know about this function through registration then we will try to return corresponding result type.
		fnName := reflect.ValueOf(f).String()
		if fnRegistered, ok := getHostEnvironment().getActivityFn(fnName); ok {
			return deSerializeFnResultFromFnType(reflect.TypeOf(fnRegistered), result, to)
		}
	}

	// For everything we return result.
	return getHostEnvironment().decodeArg(result, to)
}

func setActivityParametersIfNotExist(ctx Context) Context {
	params := getActivityOptions(ctx)
	var newParams activityOptions
	if params != nil {
		newParams = *params
	}
	return WithValue(ctx, activityOptionsContextKey, &newParams)
}

func setLocalActivityParametersIfNotExist(ctx Context) Context {
	params := getLocalActivityOptions(ctx)
	var newParams localActivityOptions
	if params != nil {
		newParams = *params
	}
	return WithValue(ctx, localActivityOptionsContextKey, &newParams)
}
