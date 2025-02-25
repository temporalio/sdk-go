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

// Code generated by mockery v1.0.0, and other versions, with manual fixups.
// Modified manually for type alias to work correctly.
// https://github.com/vektra/mockery/issues/236

package mocks

import (
	"context"

	"go.temporal.io/sdk/client"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/converter"

	"github.com/stretchr/testify/mock"

	"go.temporal.io/api/operatorservice/v1"

	"go.temporal.io/api/workflowservice/v1"
)

// Client is an autogenerated mock type for the Client type
type Client struct {
	mock.Mock
}

// CancelWorkflow provides a mock function with given fields: ctx, workflowID, runID
func (_m *Client) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	ret := _m.Called(ctx, workflowID, runID)

	if len(ret) == 0 {
		panic("no return value specified for CancelWorkflow")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) error); ok {
		r0 = rf(ctx, workflowID, runID)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// CheckHealth provides a mock function with given fields: ctx, request
func (_m *Client) CheckHealth(ctx context.Context, request *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for CheckHealth")
	}

	var r0 *client.CheckHealthResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *client.CheckHealthRequest) (*client.CheckHealthResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *client.CheckHealthRequest) *client.CheckHealthResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*client.CheckHealthResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *client.CheckHealthRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Close provides a mock function with given fields:
func (_m *Client) Close() {
	_m.Called()
}

// CompleteActivity provides a mock function with given fields: ctx, taskToken, result, err
func (_m *Client) CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error {
	ret := _m.Called(ctx, taskToken, result, err)

	if len(ret) == 0 {
		panic("no return value specified for CompleteActivity")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []byte, interface{}, error) error); ok {
		r0 = rf(ctx, taskToken, result, err)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// CompleteActivityByID provides a mock function with given fields: ctx, namespace, workflowID, runID, activityID, result, err
func (_m *Client) CompleteActivityByID(ctx context.Context, namespace string, workflowID string, runID string, activityID string, result interface{}, err error) error {
	ret := _m.Called(ctx, namespace, workflowID, runID, activityID, result, err)

	if len(ret) == 0 {
		panic("no return value specified for CompleteActivityByID")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, string, interface{}, error) error); ok {
		r0 = rf(ctx, namespace, workflowID, runID, activityID, result, err)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// CountWorkflow provides a mock function with given fields: ctx, request
func (_m *Client) CountWorkflow(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for CountWorkflow")
	}

	var r0 *workflowservice.CountWorkflowExecutionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.CountWorkflowExecutionsRequest) *workflowservice.CountWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.CountWorkflowExecutionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.CountWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// DeploymentClient provides a mock function with given fields:
//
//lint:ignore SA1019 ignore deprecated versioning APIs
func (_m *Client) DeploymentClient() client.DeploymentClient {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for DeploymentClient")
	}

	//lint:ignore SA1019 ignore deprecated versioning APIs
	var r0 client.DeploymentClient
	//lint:ignore SA1019 ignore deprecated versioning APIs
	if rf, ok := ret.Get(0).(func() client.DeploymentClient); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			//lint:ignore SA1019 ignore deprecated versioning APIs
			r0 = ret.Get(0).(client.DeploymentClient)
		}
	}

	return r0
}

// DescribeTaskQueue provides a mock function with given fields: ctx, taskqueue, taskqueueType
func (_m *Client) DescribeTaskQueue(ctx context.Context, taskqueue string, taskqueueType enums.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error) {
	ret := _m.Called(ctx, taskqueue, taskqueueType)

	if len(ret) == 0 {
		panic("no return value specified for DescribeTaskQueue")
	}

	var r0 *workflowservice.DescribeTaskQueueResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, enums.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error)); ok {
		return rf(ctx, taskqueue, taskqueueType)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, enums.TaskQueueType) *workflowservice.DescribeTaskQueueResponse); ok {
		r0 = rf(ctx, taskqueue, taskqueueType)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.DescribeTaskQueueResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, enums.TaskQueueType) error); ok {
		r1 = rf(ctx, taskqueue, taskqueueType)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// DescribeTaskQueueEnhanced provides a mock function with given fields: ctx, options
func (_m *Client) DescribeTaskQueueEnhanced(ctx context.Context, options client.DescribeTaskQueueEnhancedOptions) (client.TaskQueueDescription, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for DescribeTaskQueueEnhanced")
	}

	var r0 client.TaskQueueDescription
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.DescribeTaskQueueEnhancedOptions) (client.TaskQueueDescription, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.DescribeTaskQueueEnhancedOptions) client.TaskQueueDescription); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.TaskQueueDescription)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.DescribeTaskQueueEnhancedOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// DescribeWorkflowExecution provides a mock function with given fields: ctx, workflowID, runID
func (_m *Client) DescribeWorkflowExecution(ctx context.Context, workflowID string, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	ret := _m.Called(ctx, workflowID, runID)

	if len(ret) == 0 {
		panic("no return value specified for DescribeWorkflowExecution")
	}

	var r0 *workflowservice.DescribeWorkflowExecutionResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error)); ok {
		return rf(ctx, workflowID, runID)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string) *workflowservice.DescribeWorkflowExecutionResponse); ok {
		r0 = rf(ctx, workflowID, runID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.DescribeWorkflowExecutionResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string) error); ok {
		r1 = rf(ctx, workflowID, runID)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ExecuteWorkflow provides a mock function with given fields: ctx, options, workflow, args
func (_m *Client) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	var _ca []interface{}
	_ca = append(_ca, ctx, options, workflow)
	_ca = append(_ca, args...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for ExecuteWorkflow")
	}

	var r0 client.WorkflowRun
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)); ok {
		return rf(ctx, options, workflow, args...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) client.WorkflowRun); ok {
		r0 = rf(ctx, options, workflow, args...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowRun)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.StartWorkflowOptions, interface{}, ...interface{}) error); ok {
		r1 = rf(ctx, options, workflow, args...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetSearchAttributes provides a mock function with given fields: ctx
func (_m *Client) GetSearchAttributes(ctx context.Context) (*workflowservice.GetSearchAttributesResponse, error) {
	ret := _m.Called(ctx)

	if len(ret) == 0 {
		panic("no return value specified for GetSearchAttributes")
	}

	var r0 *workflowservice.GetSearchAttributesResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context) (*workflowservice.GetSearchAttributesResponse, error)); ok {
		return rf(ctx)
	}
	if rf, ok := ret.Get(0).(func(context.Context) *workflowservice.GetSearchAttributesResponse); ok {
		r0 = rf(ctx)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.GetSearchAttributesResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetWorkerBuildIdCompatibility provides a mock function with given fields: ctx, options
//
//lint:ignore SA1019 ignore for SDK
func (_m *Client) GetWorkerBuildIdCompatibility(ctx context.Context, options *client.GetWorkerBuildIdCompatibilityOptions) (*client.WorkerBuildIDVersionSets, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkerBuildIdCompatibility")
	}

	//lint:ignore SA1019 ignore for SDK mocks
	var r0 *client.WorkerBuildIDVersionSets
	var r1 error
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(0).(func(context.Context, *client.GetWorkerBuildIdCompatibilityOptions) (*client.WorkerBuildIDVersionSets, error)); ok {
		return rf(ctx, options)
	}
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(0).(func(context.Context, *client.GetWorkerBuildIdCompatibilityOptions) *client.WorkerBuildIDVersionSets); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			//lint:ignore SA1019 ignore for SDK mocks
			r0 = ret.Get(0).(*client.WorkerBuildIDVersionSets)
		}
	}
	//lint:ignore SA1019 ignore for SDK
	if rf, ok := ret.Get(1).(func(context.Context, *client.GetWorkerBuildIdCompatibilityOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetWorkerTaskReachability provides a mock function with given fields: ctx, options
//
//lint:ignore SA1019 ignore for SDK mocks
func (_m *Client) GetWorkerTaskReachability(ctx context.Context, options *client.GetWorkerTaskReachabilityOptions) (*client.WorkerTaskReachability, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkerTaskReachability")
	}
	//lint:ignore SA1019 ignore for SDK mocks
	var r0 *client.WorkerTaskReachability
	var r1 error
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(0).(func(context.Context, *client.GetWorkerTaskReachabilityOptions) (*client.WorkerTaskReachability, error)); ok {
		return rf(ctx, options)
	}
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(0).(func(context.Context, *client.GetWorkerTaskReachabilityOptions) *client.WorkerTaskReachability); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			//lint:ignore SA1019 ignore for SDK mocks
			r0 = ret.Get(0).(*client.WorkerTaskReachability)
		}
	}
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(1).(func(context.Context, *client.GetWorkerTaskReachabilityOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetWorkerVersioningRules provides a mock function with given fields: ctx, options
func (_m *Client) GetWorkerVersioningRules(ctx context.Context, options client.GetWorkerVersioningOptions) (*client.WorkerVersioningRules, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkerVersioningRules")
	}

	var r0 *client.WorkerVersioningRules
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.GetWorkerVersioningOptions) (*client.WorkerVersioningRules, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.GetWorkerVersioningOptions) *client.WorkerVersioningRules); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*client.WorkerVersioningRules)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.GetWorkerVersioningOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetWorkflow provides a mock function with given fields: ctx, workflowID, runID
func (_m *Client) GetWorkflow(ctx context.Context, workflowID string, runID string) client.WorkflowRun {
	ret := _m.Called(ctx, workflowID, runID)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkflow")
	}

	var r0 client.WorkflowRun
	if rf, ok := ret.Get(0).(func(context.Context, string, string) client.WorkflowRun); ok {
		r0 = rf(ctx, workflowID, runID)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowRun)
		}
	}

	return r0
}

// GetWorkflowHistory provides a mock function with given fields: ctx, workflowID, runID, isLongPoll, filterType
func (_m *Client) GetWorkflowHistory(ctx context.Context, workflowID string, runID string, isLongPoll bool, filterType enums.HistoryEventFilterType) client.HistoryEventIterator {
	ret := _m.Called(ctx, workflowID, runID, isLongPoll, filterType)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkflowHistory")
	}

	var r0 client.HistoryEventIterator
	if rf, ok := ret.Get(0).(func(context.Context, string, string, bool, enums.HistoryEventFilterType) client.HistoryEventIterator); ok {
		r0 = rf(ctx, workflowID, runID, isLongPoll, filterType)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.HistoryEventIterator)
		}
	}

	return r0
}

// GetWorkflowUpdateHandle provides a mock function with given fields: ref
func (_m *Client) GetWorkflowUpdateHandle(ref client.GetWorkflowUpdateHandleOptions) client.WorkflowUpdateHandle {
	ret := _m.Called(ref)

	if len(ret) == 0 {
		panic("no return value specified for GetWorkflowUpdateHandle")
	}

	var r0 client.WorkflowUpdateHandle
	if rf, ok := ret.Get(0).(func(client.GetWorkflowUpdateHandleOptions) client.WorkflowUpdateHandle); ok {
		r0 = rf(ref)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowUpdateHandle)
		}
	}

	return r0
}

// ListArchivedWorkflow provides a mock function with given fields: ctx, request
func (_m *Client) ListArchivedWorkflow(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ListArchivedWorkflow")
	}

	var r0 *workflowservice.ListArchivedWorkflowExecutionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListArchivedWorkflowExecutionsRequest) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListArchivedWorkflowExecutionsRequest) *workflowservice.ListArchivedWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.ListArchivedWorkflowExecutionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ListArchivedWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ListClosedWorkflow provides a mock function with given fields: ctx, request
func (_m *Client) ListClosedWorkflow(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ListClosedWorkflow")
	}

	var r0 *workflowservice.ListClosedWorkflowExecutionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListClosedWorkflowExecutionsRequest) (*workflowservice.ListClosedWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListClosedWorkflowExecutionsRequest) *workflowservice.ListClosedWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.ListClosedWorkflowExecutionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ListClosedWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ListOpenWorkflow provides a mock function with given fields: ctx, request
func (_m *Client) ListOpenWorkflow(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ListOpenWorkflow")
	}

	var r0 *workflowservice.ListOpenWorkflowExecutionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListOpenWorkflowExecutionsRequest) (*workflowservice.ListOpenWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListOpenWorkflowExecutionsRequest) *workflowservice.ListOpenWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.ListOpenWorkflowExecutionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ListOpenWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ListWorkflow provides a mock function with given fields: ctx, request
func (_m *Client) ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ListWorkflow")
	}

	var r0 *workflowservice.ListWorkflowExecutionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) *workflowservice.ListWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.ListWorkflowExecutionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// NewWithStartWorkflowOperation provides a mock function with given fields: options, workflow, args
func (_m *Client) NewWithStartWorkflowOperation(options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) client.WithStartWorkflowOperation {
	var _ca []interface{}
	_ca = append(_ca, options, workflow)
	_ca = append(_ca, args...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for NewWithStartWorkflowOperation")
	}

	var r0 client.WithStartWorkflowOperation
	if rf, ok := ret.Get(0).(func(client.StartWorkflowOptions, interface{}, ...interface{}) client.WithStartWorkflowOperation); ok {
		r0 = rf(options, workflow, args...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WithStartWorkflowOperation)
		}
	}

	return r0
}

// OperatorService provides a mock function with given fields:
func (_m *Client) OperatorService() operatorservice.OperatorServiceClient {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for OperatorService")
	}

	var r0 operatorservice.OperatorServiceClient
	if rf, ok := ret.Get(0).(func() operatorservice.OperatorServiceClient); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(operatorservice.OperatorServiceClient)
		}
	}

	return r0
}

// QueryWorkflow provides a mock function with given fields: ctx, workflowID, runID, queryType, args
func (_m *Client) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	var _ca []interface{}
	_ca = append(_ca, ctx, workflowID, runID, queryType)
	_ca = append(_ca, args...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for QueryWorkflow")
	}

	var r0 converter.EncodedValue
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, ...interface{}) (converter.EncodedValue, error)); ok {
		return rf(ctx, workflowID, runID, queryType, args...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, ...interface{}) converter.EncodedValue); ok {
		r0 = rf(ctx, workflowID, runID, queryType, args...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(converter.EncodedValue)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string, string, ...interface{}) error); ok {
		r1 = rf(ctx, workflowID, runID, queryType, args...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// QueryWorkflowWithOptions provides a mock function with given fields: ctx, request
func (_m *Client) QueryWorkflowWithOptions(ctx context.Context, request *client.QueryWorkflowWithOptionsRequest) (*client.QueryWorkflowWithOptionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for QueryWorkflowWithOptions")
	}

	var r0 *client.QueryWorkflowWithOptionsResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *client.QueryWorkflowWithOptionsRequest) (*client.QueryWorkflowWithOptionsResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *client.QueryWorkflowWithOptionsRequest) *client.QueryWorkflowWithOptionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*client.QueryWorkflowWithOptionsResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *client.QueryWorkflowWithOptionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RecordActivityHeartbeat provides a mock function with given fields: ctx, taskToken, details
func (_m *Client) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	var _ca []interface{}
	_ca = append(_ca, ctx, taskToken)
	_ca = append(_ca, details...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for RecordActivityHeartbeat")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, []byte, ...interface{}) error); ok {
		r0 = rf(ctx, taskToken, details...)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// RecordActivityHeartbeatByID provides a mock function with given fields: ctx, namespace, workflowID, runID, activityID, details
func (_m *Client) RecordActivityHeartbeatByID(ctx context.Context, namespace string, workflowID string, runID string, activityID string, details ...interface{}) error {
	var _ca []interface{}
	_ca = append(_ca, ctx, namespace, workflowID, runID, activityID)
	_ca = append(_ca, details...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for RecordActivityHeartbeatByID")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, string, ...interface{}) error); ok {
		r0 = rf(ctx, namespace, workflowID, runID, activityID, details...)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// ResetWorkflowExecution provides a mock function with given fields: ctx, request
func (_m *Client) ResetWorkflowExecution(ctx context.Context, request *workflowservice.ResetWorkflowExecutionRequest) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ResetWorkflowExecution")
	}

	var r0 *workflowservice.ResetWorkflowExecutionResponse
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ResetWorkflowExecutionRequest) (*workflowservice.ResetWorkflowExecutionResponse, error)); ok {
		return rf(ctx, request)
	}
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ResetWorkflowExecutionRequest) *workflowservice.ResetWorkflowExecutionResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*workflowservice.ResetWorkflowExecutionResponse)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ResetWorkflowExecutionRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ScanWorkflow provides a mock function with given fields: ctx, request
//
//lint:ignore SA1019 the server API was deprecated.
func (_m *Client) ScanWorkflow(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	ret := _m.Called(ctx, request)

	if len(ret) == 0 {
		panic("no return value specified for ScanWorkflow")
	}

	//lint:ignore SA1019 the server API was deprecated.
	var r0 *workflowservice.ScanWorkflowExecutionsResponse
	var r1 error
	//lint:ignore SA1019 the server API was deprecated.
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ScanWorkflowExecutionsRequest) (*workflowservice.ScanWorkflowExecutionsResponse, error)); ok {
		return rf(ctx, request)
	}
	//lint:ignore SA1019 the server API was deprecated.
	if rf, ok := ret.Get(0).(func(context.Context, *workflowservice.ScanWorkflowExecutionsRequest) *workflowservice.ScanWorkflowExecutionsResponse); ok {
		r0 = rf(ctx, request)
	} else {
		if ret.Get(0) != nil {
			//lint:ignore SA1019 the server API was deprecated.
			r0 = ret.Get(0).(*workflowservice.ScanWorkflowExecutionsResponse)
		}
	}

	//lint:ignore SA1019 the server API was deprecated.
	if rf, ok := ret.Get(1).(func(context.Context, *workflowservice.ScanWorkflowExecutionsRequest) error); ok {
		r1 = rf(ctx, request)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// ScheduleClient provides a mock function with given fields:
func (_m *Client) ScheduleClient() client.ScheduleClient {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for ScheduleClient")
	}

	var r0 client.ScheduleClient
	if rf, ok := ret.Get(0).(func() client.ScheduleClient); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.ScheduleClient)
		}
	}

	return r0
}

// SignalWithStartWorkflow provides a mock function with given fields: ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs
func (_m *Client) SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{}, options client.StartWorkflowOptions, workflow interface{}, workflowArgs ...interface{}) (client.WorkflowRun, error) {
	var _ca []interface{}
	_ca = append(_ca, ctx, workflowID, signalName, signalArg, options, workflow)
	_ca = append(_ca, workflowArgs...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for SignalWithStartWorkflow")
	}

	var r0 client.WorkflowRun
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, interface{}, client.StartWorkflowOptions, interface{}, ...interface{}) (client.WorkflowRun, error)); ok {
		return rf(ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs...)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string, interface{}, client.StartWorkflowOptions, interface{}, ...interface{}) client.WorkflowRun); ok {
		r0 = rf(ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowRun)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string, interface{}, client.StartWorkflowOptions, interface{}, ...interface{}) error); ok {
		r1 = rf(ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs...)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// SignalWorkflow provides a mock function with given fields: ctx, workflowID, runID, signalName, arg
func (_m *Client) SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error {
	ret := _m.Called(ctx, workflowID, runID, signalName, arg)

	if len(ret) == 0 {
		panic("no return value specified for SignalWorkflow")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, interface{}) error); ok {
		r0 = rf(ctx, workflowID, runID, signalName, arg)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// TerminateWorkflow provides a mock function with given fields: ctx, workflowID, runID, reason, details
func (_m *Client) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error {
	var _ca []interface{}
	_ca = append(_ca, ctx, workflowID, runID, reason)
	_ca = append(_ca, details...)
	ret := _m.Called(_ca...)

	if len(ret) == 0 {
		panic("no return value specified for TerminateWorkflow")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, string, ...interface{}) error); ok {
		r0 = rf(ctx, workflowID, runID, reason, details...)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UpdateWithStartWorkflow provides a mock function with given fields: ctx, options
func (_m *Client) UpdateWithStartWorkflow(ctx context.Context, options client.UpdateWithStartWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for UpdateWithStartWorkflow")
	}

	var r0 client.WorkflowUpdateHandle
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWithStartWorkflowOptions) (client.WorkflowUpdateHandle, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWithStartWorkflowOptions) client.WorkflowUpdateHandle); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowUpdateHandle)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.UpdateWithStartWorkflowOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// UpdateWorkerBuildIdCompatibility provides a mock function with given fields: ctx, options
//
//lint:ignore SA1019 ignore for SDK mocks
func (_m *Client) UpdateWorkerBuildIdCompatibility(ctx context.Context, options *client.UpdateWorkerBuildIdCompatibilityOptions) error {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for UpdateWorkerBuildIdCompatibility")
	}

	var r0 error
	//lint:ignore SA1019 ignore for SDK mocks
	if rf, ok := ret.Get(0).(func(context.Context, *client.UpdateWorkerBuildIdCompatibilityOptions) error); ok {
		r0 = rf(ctx, options)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UpdateWorkerVersioningRules provides a mock function with given fields: ctx, options
func (_m *Client) UpdateWorkerVersioningRules(ctx context.Context, options client.UpdateWorkerVersioningRulesOptions) (*client.WorkerVersioningRules, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for UpdateWorkerVersioningRules")
	}

	var r0 *client.WorkerVersioningRules
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkerVersioningRulesOptions) (*client.WorkerVersioningRules, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkerVersioningRulesOptions) *client.WorkerVersioningRules); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*client.WorkerVersioningRules)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.UpdateWorkerVersioningRulesOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// UpdateWorkflow provides a mock function with given fields: ctx, options
func (_m *Client) UpdateWorkflow(ctx context.Context, options client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for UpdateWorkflow")
	}

	var r0 client.WorkflowUpdateHandle
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkflowOptions) client.WorkflowUpdateHandle); ok {
		r0 = rf(ctx, options)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkflowUpdateHandle)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.UpdateWorkflowOptions) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// UpdateWorkflowExecutionOptions provides a mock function with given fields: ctx, options
func (_m *Client) UpdateWorkflowExecutionOptions(ctx context.Context, options client.UpdateWorkflowExecutionOptionsRequest) (client.WorkflowExecutionOptions, error) {
	ret := _m.Called(ctx, options)

	if len(ret) == 0 {
		panic("no return value specified for UpdateWorkflowExecutionOptions")
	}

	var r0 client.WorkflowExecutionOptions
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkflowExecutionOptionsRequest) (client.WorkflowExecutionOptions, error)); ok {
		return rf(ctx, options)
	}
	if rf, ok := ret.Get(0).(func(context.Context, client.UpdateWorkflowExecutionOptionsRequest) client.WorkflowExecutionOptions); ok {
		r0 = rf(ctx, options)
	} else {
		r0 = ret.Get(0).(client.WorkflowExecutionOptions)
	}

	if rf, ok := ret.Get(1).(func(context.Context, client.UpdateWorkflowExecutionOptionsRequest) error); ok {
		r1 = rf(ctx, options)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// WorkerDeploymentClient provides a mock function with given fields:
func (_m *Client) WorkerDeploymentClient() client.WorkerDeploymentClient {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for WorkerDeploymentClient")
	}

	var r0 client.WorkerDeploymentClient
	if rf, ok := ret.Get(0).(func() client.WorkerDeploymentClient); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.WorkerDeploymentClient)
		}
	}

	return r0
}

// WorkflowService provides a mock function with given fields:
func (_m *Client) WorkflowService() workflowservice.WorkflowServiceClient {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for WorkflowService")
	}

	var r0 workflowservice.WorkflowServiceClient
	if rf, ok := ret.Get(0).(func() workflowservice.WorkflowServiceClient); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(workflowservice.WorkflowServiceClient)
		}
	}

	return r0
}

// NewClient creates a new instance of Client. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewClient(t interface {
	mock.TestingT
	Cleanup(func())
}) *Client {
	mock := &Client{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
