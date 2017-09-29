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

package cadence

import (
	"context"
	"time"

	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/cadence"
	s "go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common/metrics"
)

// QueryTypeStackTrace is the build in query type for Client.QueryWorkflow() call. Use this query type to get the call
// stack of the workflow. The result will be a string encoded in the EncodedValue.
const QueryTypeStackTrace string = "__stack_trace"

type (
	// Client is the client for starting and getting information about a workflow executions as well as
	// completing activities asynchronously.
	Client interface {
		// StartWorkflow starts a workflow execution
		// The user can use this to start using a function or workflow type name.
		// Either by
		//     StartWorkflow(ctx, options, "workflowTypeName", input)
		//     or
		//     StartWorkflow(ctx, options, workflowExecuteFn, arg1, arg2, arg3)
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- WorkflowExecutionAlreadyStartedError
		StartWorkflow(ctx context.Context, options StartWorkflowOptions, workflow interface{}, args ...interface{}) (*WorkflowExecution, error)

		// SignalWorkflow sends a signals to a workflow in execution
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - signalName name to identify the signal.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- InternalServiceError
		SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error

		// CancelWorkflow cancels a workflow in execution
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		CancelWorkflow(ctx context.Context, workflowID string, runID string) error

		// TerminateWorkflow terminates a workflow execution.
		// workflowID is required, other parameters are optional.
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details []byte) error

		// GetWorkflowHistory gets history of a particular workflow.
		// - workflow ID of the workflow.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		GetWorkflowHistory(ctx context.Context, workflowID string, runID string) (*s.History, error)

		// GetWorkflowStackTrace gets a stack trace of all goroutines of a particular workflow.
		// atDecisionTaskCompletedEventID is the eventID of the CompleteDecisionTask event at which stack trace should be taken.
		// It allows to look at the past states of a workflow.
		// 0 value indicates that the whole existing history should be used.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		GetWorkflowStackTrace(ctx context.Context, workflowID string, runID string, atDecisionTaskCompletedEventID int64) (string, error)

		// CompleteActivity reports activity completed.
		// activity Execute method can return cadence.ErrActivityResultPending to
		// indicate the activity is not completed when it's Execute method returns. In that case, this CompleteActivity() method
		// should be called when that activity is completed with the actual result and error. If err is nil, activity task
		// completed event will be reported; if err is CanceledError, activity task cancelled event will be reported; otherwise,
		// activity task failed event will be reported.
		// An activity implementation should use GetActivityInfo(ctx).TaskToken function to get task token to use for completion.
		// Example:-
		//	To complete with a result.
		//  	CompleteActivity(token, "Done", nil)
		//	To fail the activity with an error.
		//      CompleteActivity(token, nil, NewErrorWithDetails("reason", details)
		// The activity can fail with below errors ErrorWithDetails, TimeoutError, CanceledError.
		CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error

		// RecordActivityHeartbeat records heartbeat for an activity.
		// details - is the progress you want to record along with heart beat for this activity.
		// The errors it can return:
		//	- EntityNotExistsError
		//	- InternalServiceError
		RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error

		// ListClosedWorkflow gets closed workflow executions based on request filters
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		ListClosedWorkflow(ctx context.Context, request *s.ListClosedWorkflowExecutionsRequest) (*s.ListClosedWorkflowExecutionsResponse, error)

		// ListClosedWorkflow gets open workflow executions based on request filters
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		ListOpenWorkflow(ctx context.Context, request *s.ListOpenWorkflowExecutionsRequest) (*s.ListOpenWorkflowExecutionsResponse, error)

		// QueryWorkflow queries a given workflow execution and returns the query result synchronously. Parameter workflowID
		// and queryType are required, other parameters are optional. The workflowID and runID (optional) identify the
		// target workflow execution that this query will be send to. If runID is not specified (empty string), server will
		// use the currently running execution of that workflowID. The queryType specifies the type of query you want to
		// run. By default, cadence supports "__stack_trace" as a standard query type, which will return string value
		// representing the call stack of the target workflow. The target workflow could also setup different query handler
		// to handle custom query types.
		// See comments at cadence.SetQueryHandler(ctx Context, queryType string, handler interface{}) for more details
		// on how to setup query handler within the target workflow.
		// - workflowID is required.
		// - runID can be default(empty string). if empty string then it will pick the running execution of that workflow ID.
		// - queryType is the type of the query.
		// - args... are the optional query parameters.
		// The errors it can return:
		//  - BadRequestError
		//  - InternalServiceError
		//  - EntityNotExistError
		//  - QueryFailError
		QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (EncodedValue, error)
	}

	// ClientOptions are optional parameters for Client creation.
	ClientOptions struct {
		MetricsScope tally.Scope
		Identity     string
	}

	// StartWorkflowOptions configuration parameters for starting a workflow execution.
	StartWorkflowOptions struct {
		// ID - The business identifier of the workflow execution.
		// Optional: defaulted to a uuid.
		ID string

		// TaskList - The decisions of the workflow are scheduled on this queue.
		// This is also the default task list on which activities are scheduled. The workflow author can choose
		// to override this using activity options.
		// Mandatory: No default.
		TaskList string

		// ExecutionStartToCloseTimeout - The time out for duration of workflow execution.
		// The resolution is seconds.
		// Mandatory: No default.
		ExecutionStartToCloseTimeout time.Duration

		// DecisionTaskStartToCloseTimeout - The time out for processing decision task from the time the worker
		// pulled this task.
		// The resolution is seconds.
		// Optional: defaulted to 20 secs.
		DecisionTaskStartToCloseTimeout time.Duration
	}

	// DomainClient is the client for managing operations on the domain.
	// CLI, tools, ... can use this layer to manager operations on domain.
	DomainClient interface {
		// Register a domain with cadence server
		// The errors it can throw:
		//	- DomainAlreadyExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Register(ctx context.Context, request *s.RegisterDomainRequest) error

		// Describe a domain. The domain has two part of information.
		// DomainInfo - Which has Name, Status, Description, Owner Email.
		// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
		// The errors it can throw:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Describe(ctx context.Context, name string) (*s.DomainInfo, *s.DomainConfiguration, error)

		// Update a domain. The domain has two part of information.
		// UpdateDomainInfo - To update domain Description and Owner Email.
		// DomainConfiguration - Configuration like Workflow Execution Retention Period In Days, Whether to emit metrics.
		// The errors it can throw:
		//	- EntityNotExistsError
		//	- BadRequestError
		//	- InternalServiceError
		Update(ctx context.Context, name string, domainInfo *s.UpdateDomainInfo, domainConfig *s.DomainConfiguration) error
	}
)

// NewClient creates an instance of a workflow client
func NewClient(service m.TChanWorkflowService, domain string, options *ClientOptions) Client {
	var identity string
	if options == nil || options.Identity == "" {
		identity = getWorkerIdentity("")
	} else {
		identity = options.Identity
	}
	var metricScope tally.Scope
	if options != nil {
		metricScope = options.MetricsScope
	}
	metricScope = tagScope(metricScope, tagDomain, domain)
	return &workflowClient{
		workflowService: metrics.NewWorkflowServiceWrapper(service, metricScope),
		domain:          domain,
		metricsScope:    metricScope,
		identity:        identity,
	}
}

// NewDomainClient creates an instance of a domain client, to manager lifecycle of domains.
func NewDomainClient(service m.TChanWorkflowService, options *ClientOptions) DomainClient {
	var identity string
	if options == nil || options.Identity == "" {
		identity = getWorkerIdentity("")
	} else {
		identity = options.Identity
	}
	var metricScope tally.Scope
	if options != nil {
		metricScope = options.MetricsScope
	}
	metricScope = tagScope(metricScope, tagDomain, "domain-client")
	return &domainClient{
		workflowService: metrics.NewWorkflowServiceWrapper(service, metricScope),
		metricsScope:    metricScope,
		identity:        identity,
	}
}
