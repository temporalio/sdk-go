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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/temporal-proto/common"
	failurepb "go.temporal.io/temporal-proto/failure"
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
	data, err := encodeArgs(getDefaultDataConverter(), details)
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
	data, err := encodeArg(getDefaultDataConverter(), heartbeatDetail)
	if err != nil {
		panic(err)
	}
	var res string
	require.NoError(t, NewValue(data).Get(&res))
	require.Equal(t, res, heartbeatDetail)
}

func TestConvertFailureToError_CustomError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	customErr1 := NewCustomError(customErrReasonA, true, val)
	failure := convertErrorToFailure(customErr1, dc)
	require.Equal(t, customErrReasonA, failure.GetMessage())
	require.Equal(t, val.values, failure.GetApplicationFailureInfo().GetDetails())

	customErr2 := NewCustomError(customErrReasonA, true, testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	failure = convertErrorToFailure(customErr2, dc)
	require.Equal(t, customErrReasonA, failure.GetMessage())
	require.Equal(t, val2, failure.GetApplicationFailureInfo().GetDetails())
}

func TestConvertFailureToError_CancelError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	canceledErr1 := NewCanceledError(val)
	failure := convertErrorToFailure(canceledErr1, dc)
	require.NotNil(t, failure.GetCanceledFailureInfo())
	require.Equal(t, val.values, failure.GetCanceledFailureInfo().GetDetails())

	canceledErr2 := NewCanceledError(testErrorDetails1)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails1})
	require.NoError(t, err)
	failure = convertErrorToFailure(canceledErr2, dc)
	require.NotNil(t, failure.GetCanceledFailureInfo())
	require.Equal(t, val2, failure.GetCanceledFailureInfo().GetDetails())
}

func TestConvertErrorToFailure_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData("error details")
	require.NoError(t, err)

	val := newEncodedValues(details, dc).(*EncodedValues)
	timeoutErr1 := NewTimeoutError(commonpb.TimeoutType_ScheduleToStart, nil, val)
	failure := convertErrorToFailure(timeoutErr1, dc)
	require.NotNil(t, failure.GetTimeoutFailureInfo())
	require.Equal(t, commonpb.TimeoutType_ScheduleToStart, failure.GetTimeoutFailureInfo().GetTimeoutType())
	require.Equal(t, val.values, failure.GetTimeoutFailureInfo().GetLastHeartbeatDetails())

	timeoutErr2 := NewTimeoutError(commonpb.TimeoutType_Heartbeat, nil, testErrorDetails4)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails4})
	require.NoError(t, err)
	failure = convertErrorToFailure(timeoutErr2, dc)
	require.NotNil(t, failure.GetTimeoutFailureInfo())
	require.Equal(t, commonpb.TimeoutType_Heartbeat, failure.GetTimeoutFailureInfo().GetTimeoutType())
	require.Equal(t, val2, failure.GetTimeoutFailureInfo().GetLastHeartbeatDetails())
}

func TestConvertFailureToError_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData(testErrorDetails1)
	require.NoError(t, err)

	failure := &failurepb.Failure{
		FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{
			TimeoutType:          commonpb.TimeoutType_Heartbeat,
			LastHeartbeatDetails: details,
		}},
	}
	constructedErr := convertFailureToError(failure, dc)
	timeoutErr, ok := constructedErr.(*TimeoutError)
	require.True(t, ok)
	require.True(t, timeoutErr.HasLastHeartbeatDetails())
	var detailValue string
	err = timeoutErr.LastHeartbeatDetails(&detailValue)
	require.NoError(t, err)
	require.Equal(t, testErrorDetails1, detailValue)
}
