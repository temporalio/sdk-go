package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"

	"go.temporal.io/sdk/converter"
)

func TestChannelBuilderOptions(t *testing.T) {
	t.Parallel()
	builder := &grpcContextBuilder{Timeout: defaultRPCTimeout}

	opt1 := grpcTimeout(time.Minute)
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
	data, err := encodeArgs(converter.GetDefaultDataConverter(), details)
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
	data, err := encodeArg(converter.GetDefaultDataConverter(), heartbeatDetail)
	if err != nil {
		panic(err)
	}
	var res string
	require.NoError(t, NewValue(data).Get(&res))
	require.Equal(t, res, heartbeatDetail)
}

func TestFailureToError_ApplicationError(t *testing.T) {
	t.Parallel()
	dc := converter.GetDefaultDataConverter()
	fc := GetDefaultFailureConverter()
	details, err := dc.ToPayloads("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	applicationErr1 := NewApplicationError(applicationErrReasonA, "", false, nil, val)
	failure := fc.ErrorToFailure(applicationErr1)
	require.Equal(t, applicationErrReasonA, failure.GetMessage())
	require.Equal(t, val.values, failure.GetApplicationFailureInfo().GetDetails())

	applicationErr2 := NewApplicationError(applicationErrReasonA, "", false, nil, testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	failure = fc.ErrorToFailure(applicationErr2)
	require.Equal(t, applicationErrReasonA, failure.GetMessage())
	require.Equal(t, val2, failure.GetApplicationFailureInfo().GetDetails())
}

func TestFailureToError_CancelError(t *testing.T) {
	t.Parallel()
	dc := converter.GetDefaultDataConverter()
	fc := GetDefaultFailureConverter()
	details, err := dc.ToPayloads("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	canceledErr1 := NewCanceledError(val)
	failure := fc.ErrorToFailure(canceledErr1)
	require.NotNil(t, failure.GetCanceledFailureInfo())
	require.Equal(t, val.values, failure.GetCanceledFailureInfo().GetDetails())

	canceledErr2 := NewCanceledError(testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	failure = fc.ErrorToFailure(canceledErr2)
	require.NotNil(t, failure.GetCanceledFailureInfo())
	require.Equal(t, val2, failure.GetCanceledFailureInfo().GetDetails())
}

func TestErrorToFailure_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := converter.GetDefaultDataConverter()
	fc := GetDefaultFailureConverter()
	details, err := dc.ToPayloads("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	timeoutErr1 := NewTimeoutError("timeout", enumspb.TIMEOUT_TYPE_SCHEDULE_TO_START, nil, val)
	failure := fc.ErrorToFailure(timeoutErr1)
	require.NotNil(t, failure.GetTimeoutFailureInfo())
	require.Equal(t, enumspb.TIMEOUT_TYPE_SCHEDULE_TO_START, failure.GetTimeoutFailureInfo().GetTimeoutType())
	require.Equal(t, val.values, failure.GetTimeoutFailureInfo().GetLastHeartbeatDetails())

	timeoutErr2 := NewTimeoutError("timeout", enumspb.TIMEOUT_TYPE_HEARTBEAT, nil, testErrorDetails4)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails4})
	require.NoError(t, err)
	failure = fc.ErrorToFailure(timeoutErr2)
	require.NotNil(t, failure.GetTimeoutFailureInfo())
	require.Equal(t, enumspb.TIMEOUT_TYPE_HEARTBEAT, failure.GetTimeoutFailureInfo().GetTimeoutType())
	require.Equal(t, val2, failure.GetTimeoutFailureInfo().GetLastHeartbeatDetails())
}

func TestFailureToError_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := converter.GetDefaultDataConverter()
	fc := GetDefaultFailureConverter()
	details, err := dc.ToPayloads(testErrorDetails1)
	require.NoError(t, err)

	failure := &failurepb.Failure{
		FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{
			TimeoutType:          enumspb.TIMEOUT_TYPE_HEARTBEAT,
			LastHeartbeatDetails: details,
		}},
	}
	constructedErr := fc.FailureToError(failure)
	timeoutErr, ok := constructedErr.(*TimeoutError)
	require.True(t, ok)
	require.True(t, timeoutErr.HasLastHeartbeatDetails())
	var detailValue string
	err = timeoutErr.LastHeartbeatDetails(&detailValue)
	require.NoError(t, err)
	require.Equal(t, testErrorDetails1, detailValue)
}
