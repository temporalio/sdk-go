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

// Package client contains functions to create Cadence clients used to communicate to Cadence service.
//
// Use these to perform CRUD on domains and start or query workflow executions.
package client

import (
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/internal"
)

// QueryTypeStackTrace is the build in query type for Client.QueryWorkflow() call. Use this query type to get the call
// stack of the workflow. The result will be a string encoded in the encoded.Value.
const QueryTypeStackTrace string = internal.QueryTypeStackTrace

type (
	// Client is the client for starting and getting information about a workflow executions as well as
	// completing activities asynchronously.
	Client = internal.Client

	// Options are optional parameters for Client creation.
	Options = internal.ClientOptions

	// StartWorkflowOptions configuration parameters for starting a workflow execution.
	StartWorkflowOptions = internal.StartWorkflowOptions

	// DomainClient is the client for managing operations on the domain.
	// CLI, tools, ... can use this layer to manager operations on domain.
	DomainClient = internal.DomainClient

	// HistoryEventIterator is a iterator which can return history events
	HistoryEventIterator = internal.HistoryEventIterator

	// WorkflowRun represents a started non child workflow
	WorkflowRun = internal.WorkflowRun

	// WorkflowIDReusePolicy defines workflow ID reuse behavior.
	WorkflowIDReusePolicy = internal.WorkflowIDReusePolicy
)

const (
	// WorkflowIDReusePolicyAllowDuplicateFailedOnly allow start a workflow execution
	// when workflow not running, and the last execution close state is in
	// [terminated, cancelled, timeouted, failed].
	WorkflowIDReusePolicyAllowDuplicateFailedOnly WorkflowIDReusePolicy = internal.WorkflowIDReusePolicyAllowDuplicateFailedOnly

	// WorkflowIDReusePolicyAllowDuplicate allow start a workflow execution using
	// the same workflow ID,when workflow not running.
	WorkflowIDReusePolicyAllowDuplicate WorkflowIDReusePolicy = internal.WorkflowIDReusePolicyAllowDuplicate

	// WorkflowIDReusePolicyRejectDuplicate do not allow start a workflow execution using the same workflow ID at all
	WorkflowIDReusePolicyRejectDuplicate WorkflowIDReusePolicy = internal.WorkflowIDReusePolicyRejectDuplicate
)

// NewClient creates an instance of a workflow client
func NewClient(service workflowserviceclient.Interface, domain string, options *Options) Client {
	return internal.NewClient(service, domain, options)
}

// NewDomainClient creates an instance of a domain client, to manage lifecycle of domains.
func NewDomainClient(service workflowserviceclient.Interface, options *Options) DomainClient {
	return internal.NewDomainClient(service, options)
}
