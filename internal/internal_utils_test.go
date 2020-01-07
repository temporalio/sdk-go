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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	s "go.uber.org/cadence/.gen/go/shared"
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
	NewValues(data).Get(&res, &res2, &res3)
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
	timeoutErr1 := NewTimeoutError(s.TimeoutTypeScheduleToStart, val)
	reason, data := getErrorDetails(timeoutErr1, dc)
	require.Equal(t, fmt.Sprintf("%v %v", errReasonTimeout, s.TimeoutTypeScheduleToStart), reason)
	require.Equal(t, val.values, data)

	timeoutErr2 := NewTimeoutError(s.TimeoutTypeHeartbeat, testErrorDetails4)
	val2, err := encodeArgs(dc, []interface{}{testErrorDetails4})
	require.NoError(t, err)
	reason, data = getErrorDetails(timeoutErr2, dc)
	require.Equal(t, fmt.Sprintf("%v %v", errReasonTimeout, s.TimeoutTypeHeartbeat), reason)
	require.Equal(t, val2, data)
}

func TestConstructError_TimeoutError(t *testing.T) {
	t.Parallel()
	dc := getDefaultDataConverter()
	details, err := dc.ToData(testErrorDetails1)
	require.NoError(t, err)

	reason := fmt.Sprintf("%v %v", errReasonTimeout, s.TimeoutTypeHeartbeat)
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
	details, err = dc.ToData(s.TimeoutTypeHeartbeat)
	constructedErr = constructError(reason, details, dc)
	timeoutErr, ok = constructedErr.(*TimeoutError)
	require.True(t, ok)
	require.Equal(t, s.TimeoutTypeHeartbeat, timeoutErr.TimeoutType())
	require.False(t, timeoutErr.HasDetails())
}
