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

	"github.com/stretchr/testify/mock"
	"go.temporal.io/api/workflowservice/v1"
)

// NamespaceClient is an autogenerated mock type for the NamespaceClient type
type NamespaceClient struct {
	mock.Mock
}

// Close provides a mock function with given fields:
func (_m *NamespaceClient) Close() {
	_m.Called()
}

// Describe provides a mock function with given fields: ctx, name
func (_m *NamespaceClient) Describe(ctx context.Context, name string) (*workflowservice.DescribeNamespaceResponse, error) {
	ret := _m.Called(ctx, name)

	if len(ret) == 0 {
		panic("no return value specified for Describe")
	}

	var r0 *workflowservice.DescribeNamespaceResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string) (*workflowservice.DescribeNamespaceResponse, error)); ok {
		return rf(ctx, name)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string) *workflowservice.DescribeNamespaceResponse); ok {
		r0 = rf(ctx, name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.DescribeNamespaceResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string) error); ok {
		r1 = rf(ctx, name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Register provides a mock function with given fields: ctx, request
func (_m *NamespaceClient) Register(ctx context.Context, request *workflowservice.RegisterNamespaceRequest) error {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for Register")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.RegisterNamespaceRequest) error); ok {
		r0 = rf(ctx, request)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Update provides a mock function with given fields: ctx, request
func (_m *NamespaceClient) Update(ctx context.Context, request *workflowservice.UpdateNamespaceRequest) error {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for Update")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.UpdateNamespaceRequest) error); ok {
		r0 = rf(ctx, request)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewNamespaceClient creates a new instance of NamespaceClient. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewNamespaceClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *NamespaceClient {
	mock := &NamespaceClient{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
