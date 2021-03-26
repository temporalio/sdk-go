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
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"

	"go.temporal.io/sdk/converter"
)

func TestGetChildWorkflowOptions(t *testing.T) {
	opts := ChildWorkflowOptions{
		Namespace:                "foo",
		WorkflowID:               "bar",
		TaskQueue:                "baz",
		WorkflowExecutionTimeout: 1,
		WorkflowRunTimeout:       2,
		WorkflowTaskTimeout:      3,
		WaitForCancellation:      true,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy:              newTestRetryPolicy(),
		CronSchedule:             "todo",
		Memo: map[string]interface{}{
			"foo": "bar",
		},
		SearchAttributes: map[string]interface{}{
			"foo": "bar",
		},
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	}

	// Require test options to have non-zero value for each field. This ensures that we update tests (and the
	// GetChildWorkflowOptions implementation) when new fields are added to the ChildWorkflowOptions struct.
	assertNonZero(t, opts)
	// Check that the same opts set on context are also extracted from context
	assert.Equal(t, opts, GetChildWorkflowOptions(WithChildWorkflowOptions(newTestWorkflowContext(), opts)))
}

func TestGetActivityOptions(t *testing.T) {
	opts := ActivityOptions{
		TaskQueue:              "foo",
		ScheduleToCloseTimeout: time.Millisecond,
		ScheduleToStartTimeout: time.Second,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Hour,
		WaitForCancellation:    true,
		ActivityID:             "bar",
		RetryPolicy:            newTestRetryPolicy(),
	}

	assertNonZero(t, opts)
	assert.Equal(t, opts, GetActivityOptions(WithActivityOptions(newTestWorkflowContext(), opts)))
}

func TestGetLocalActivityOptions(t *testing.T) {
	opts := LocalActivityOptions{
		ScheduleToCloseTimeout: time.Minute,
		StartToCloseTimeout:    time.Hour,
		RetryPolicy:            newTestRetryPolicy(),
	}

	assertNonZero(t, opts)
	assert.Equal(t, opts, GetLocalActivityOptions(WithLocalActivityOptions(newTestWorkflowContext(), opts)))
}

func TestConvertRetryPolicy(t *testing.T) {
	someDuration := time.Minute
	pbRetryPolicy := commonpb.RetryPolicy{
		InitialInterval:        &someDuration,
		MaximumInterval:        &someDuration,
		BackoffCoefficient:     1,
		MaximumAttempts:        2,
		NonRetryableErrorTypes: []string{"some_error"},
	}

	assertNonZero(t, pbRetryPolicy)
	// Check that converting from/to commonpb.RetryPolicy is transparent
	assert.Equal(t, &pbRetryPolicy, convertToPBRetryPolicy(convertFromPBRetryPolicy(&pbRetryPolicy)))
}

func newTestWorkflowContext() Context {
	return newWorkflowContext(&workflowEnvironmentImpl{
		dataConverter: converter.GetDefaultDataConverter(),
		workflowInfo: &WorkflowInfo{
			Namespace:     "default",
			TaskQueueName: "default",
		},
	}, nil, nil)
}

func newTestRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		InitialInterval:        1,
		BackoffCoefficient:     2,
		MaximumInterval:        3,
		MaximumAttempts:        4,
		NonRetryableErrorTypes: []string{"my_error"},
	}
}

// assertNonZero checks that every top level value, struct field, and item in a slice is a non-zero value.
func assertNonZero(t *testing.T, i interface{}) {
	_assertNonZero(t, i, reflect.ValueOf(i).Type().Name())
}

func _assertNonZero(t *testing.T, i interface{}, prefix string) {
	v := reflect.ValueOf(i)
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			_assertNonZero(t, v.Field(i).Interface(), fmt.Sprintf("%s.%s", prefix, v.Type().Field(i).Name))
		}
	case reflect.Slice:
		if v.Len() == 0 {
			t.Errorf("%s: value of type %T must have non-zero length", prefix, i)
		}
		for i := 0; i < v.Len(); i++ {
			_assertNonZero(t, v.Index(i).Interface(), fmt.Sprintf("%s[%d]", prefix, i))
		}
	case reflect.Ptr:
		if v.IsNil() {
			t.Errorf("%s: value of type %T must be non-nil", prefix, i)
		} else {
			_assertNonZero(t, reflect.Indirect(v).Interface(), prefix)
		}
	default:
		if v.IsZero() {
			t.Errorf("%s: value of type %T must be non-zero", prefix, i)
		}
	}
}
