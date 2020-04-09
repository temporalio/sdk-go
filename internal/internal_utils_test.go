package internal

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	eventpb "go.temporal.io/temporal-proto/event"
)

func TestChannelBuilderOptions(t *testing.T) {
	t.Parallel()
	builder := &contextBuilder{Timeout: defaultRPCTimeout}

	opt1 := chanTimeout(time.Minute)
	opt1(builder)

	require.Equal(t, time.Minute, builder.Timeout)
}

func TestNewValues(t *testing.T) {
	t.Parallel()
	var details []interface{}
	heartbeatDetail := "status-report-to-workflow"
	heartbeatDetail2 := 1
	heartbeatDetail3 := testStruct{
		Name: heartbeatDetail,
		Age:  heartbeatDetail2,
	}
	details = append(details, heartbeatDetail, heartbeatDetail2, heartbeatDetail3)
	data, err := encodeArgs(nil, details)
	if err != nil {
		panic(err)
	}
	var res string
	var res2 int
	var res3 testStruct
	_ = NewValues(data).Get(&res, &res2, &res3)
	require.Equal(t, heartbeatDetail, res)
	require.Equal(t, heartbeatDetail2, res2)
	require.Equal(t, heartbeatDetail3, res3)
}

func TestNewValue(t *testing.T) {
	t.Parallel()
	heartbeatDetail := "status-report-to-workflow"
	data, err := encodeArg(nil, heartbeatDetail)
	if err != nil {
		panic(err)
	}
	var res string
	require.NoError(t, NewValue(data).Get(&res))
	require.Equal(t, res, heartbeatDetail)
}

func TestGetErrorDetails_CustomError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	customErr1 := NewCustomError(customErrReasonA, val)
	reason, data := getErrorDetails(customErr1, dc)
	require.Equal(t, customErrReasonA, reason)
	require.Equal(t, val.values, data)

	customErr2 := NewCustomError(customErrReasonA, testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	reason, data = getErrorDetails(customErr2, dc)
	require.Equal(t, customErrReasonA, reason)
	require.Equal(t, val2, data)
}

func TestGetErrorDetails_CancelError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	canceledErr1 := NewCanceledError(val)
	reason, data := getErrorDetails(canceledErr1, dc)
	require.Equal(t, errReasonCanceled, reason)
	require.Equal(t, val.values, data)

	canceledErr2 := NewCanceledError(testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	reason, data = getErrorDetails(canceledErr2, dc)
	require.Equal(t, errReasonCanceled, reason)
	require.Equal(t, val2, data)
}

func TestGetErrorDetails_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	timeoutErr1 := NewTimeoutError(eventpb.TimeoutType_ScheduleToStart, val)
	reason, data := getErrorDetails(timeoutErr1, dc)
	require.Equal(t, fmt.Sprintf("%v %v", errReasonTimeout, eventpb.TimeoutType_ScheduleToStart), reason)
	require.Equal(t, val.values, data)

	timeoutErr2 := NewTimeoutError(eventpb.TimeoutType_Heartbeat, testErrorDetails4)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails4})
	require.NoError(t, err)
	reason, data = getErrorDetails(timeoutErr2, dc)
	require.Equal(t, fmt.Sprintf("%v %v", errReasonTimeout, eventpb.TimeoutType_Heartbeat), reason)
	require.Equal(t, val2, data)
}

func TestConstructError_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData(testErrorDetails1)
	require.NoError(t, err)

	reason := fmt.Sprintf("%v %v", errReasonTimeout, eventpb.TimeoutType_Heartbeat)
	constructedErr := constructError(reason, details, dc)
	timeoutErr, ok := constructedErr.(*TimeoutError)
	require.True(t, ok)
	require.True(t, timeoutErr.HasDetails())
	var detailValue string
	err = timeoutErr.Details(&detailValue)
	require.NoError(t, err)
	require.Equal(t, testErrorDetails1, detailValue)

	// Backward compatibility test
	reason = errReasonTimeout
	details, err = dc.ToData(eventpb.TimeoutType_Heartbeat)
	require.NoError(t, err)
	constructedErr = constructError(reason, details, dc)
	timeoutErr, ok = constructedErr.(*TimeoutError)
	require.True(t, ok)
	require.Equal(t, eventpb.TimeoutType_Heartbeat, timeoutErr.TimeoutType())
	require.False(t, timeoutErr.HasDetails())
}
