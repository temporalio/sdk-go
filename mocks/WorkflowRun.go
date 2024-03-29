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

// Code generated by mockery v1.0.0.
// Modified manually for type alias to work correctly.
// https://github.com/vektra/mockery/issues/236

package mocks

import (
	"context"
	"go.temporal.io/sdk/client"

	"github.com/stretchr/testify/mock"
)

// WorkflowRun is an autogenerated mock type for the WorkflowRun type
type WorkflowRun struct {
	mock.Mock
}

// Get provides a mock function with given fields: ctx, valuePtr
func (_m *WorkflowRun) Get(ctx context.Context, valuePtr interface{}) error {
	ret := _m.Called(ctx, valuePtr)

	if len(ret) == 0 {
		panic("no return value specified for Get")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, interface{}) error); ok {
		r0 = rf(ctx, valuePtr)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// GetID provides a mock function with given fields:
func (_m *WorkflowRun) GetID() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetID")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetRunID provides a mock function with given fields:
func (_m *WorkflowRun) GetRunID() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetRunID")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetWithOptions provides a mock function with given fields: ctx, valuePtr, options
func (_m *WorkflowRun) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	ret := _m.Called(ctx, valuePtr, options)

	if len(ret) == 0 {
		panic("no return value specified for GetWithOptions")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, interface{}, client.WorkflowRunGetOptions) error); ok {
		r0 = rf(ctx, valuePtr, options)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewWorkflowRun creates a new instance of WorkflowRun. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewWorkflowRun(t interface {
	mock.TestingT
	Cleanup(func())
}) *WorkflowRun {
	mock := &WorkflowRun{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
