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
	ctx := newWorkflowContext(&workflowEnvironmentImpl{
		dataConverter: converter.GetDefaultDataConverter(),
		workflowInfo: &WorkflowInfo{
			Namespace:     "default",
			TaskQueueName: "default",
		},
	}, nil, nil).(Context)
	opts := ChildWorkflowOptions{
		Namespace:                "foo",
		WorkflowID:               "bar",
		TaskQueue:                "baz",
		WorkflowExecutionTimeout: 1,
		WorkflowRunTimeout:       2,
		WorkflowTaskTimeout:      3,
		WaitForCancellation:      true,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &RetryPolicy{
			InitialInterval:        1,
			BackoffCoefficient:     2,
			MaximumInterval:        3,
			MaximumAttempts:        4,
			NonRetryableErrorTypes: []string{"my_error"},
		},
		CronSchedule: "todo",
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
	assert.Equal(t, opts, GetChildWorkflowOptions(WithChildWorkflowOptions(ctx, opts)))
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
