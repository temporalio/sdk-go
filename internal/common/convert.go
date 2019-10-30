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

package common

import (
	"math"

	s "go.uber.org/cadence/.gen/go/shared"
)

// Int32Ceil return the int32 ceil of a float64
func Int32Ceil(v float64) int32 {
	return int32(math.Ceil(v))
}

// Int64Ceil return the int64 ceil of a float64
func Int64Ceil(v float64) int64 {
	return int64(math.Ceil(v))
}

// Int32Ptr makes a copy and returns the pointer to an int32.
func Int32Ptr(v int32) *int32 {
	return &v
}

// Float64Ptr makes a copy and returns the pointer to a float64.
func Float64Ptr(v float64) *float64 {
	return &v
}

// Int64Ptr makes a copy and returns the pointer to an int64.
func Int64Ptr(v int64) *int64 {
	return &v
}

// StringPtr makes a copy and returns the pointer to a string.
func StringPtr(v string) *string {
	return &v
}

// BoolPtr makes a copy and returns the pointer to a string.
func BoolPtr(v bool) *bool {
	return &v
}

// TaskListPtr makes a copy and returns the pointer to a TaskList.
func TaskListPtr(v s.TaskList) *s.TaskList {
	return &v
}

// DecisionTypePtr makes a copy and returns the pointer to a DecisionType.
func DecisionTypePtr(t s.DecisionType) *s.DecisionType {
	return &t
}

// EventTypePtr makes a copy and returns the pointer to a EventType.
func EventTypePtr(t s.EventType) *s.EventType {
	return &t
}

// QueryTaskCompletedTypePtr makes a copy and returns the pointer to a QueryTaskCompletedType.
func QueryTaskCompletedTypePtr(t s.QueryTaskCompletedType) *s.QueryTaskCompletedType {
	return &t
}

// TaskListKindPtr makes a copy and returns the pointer to a TaskListKind.
func TaskListKindPtr(t s.TaskListKind) *s.TaskListKind {
	return &t
}

// QueryResultTypePtr makes a copy and returns the pointer to a QueryResultType.
func QueryResultTypePtr(t s.QueryResultType) *s.QueryResultType {
	return &t
}
