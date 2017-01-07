package cadence

import (
	"fmt"
)

const workflowEnvironmentContextKey = "workflowEnv"
const workflowResultContextKey = "workflowResult"

// Error to return from Workflow and Activity implementations.
type Error interface {
	error
	Reason() string
	Details() []byte
}

// NewError creates Error instance
func NewError(reason string, details []byte) Error {
	return &errorImpl{reason: reason, details: details}
}

// Pointer to pointer to workflow result
func getWorkflowResultPointerPointer(ctx Context) **workflowResult {
	rpp := ctx.Value(workflowResultContextKey)
	if rpp == nil {
		panic("getWorkflowResultPointerPointer: Not a workflow context")
	}
	return rpp.(**workflowResult)
}

func getWorkflowEnvironment(ctx Context) workflowEnvironment {
	wc := ctx.Value(workflowEnvironmentContextKey)
	if wc == nil {
		panic("getWorkflowContext: Not a workflow context")
	}
	return wc.(workflowEnvironment)
}

// Workflow is an interface that any workflow should implement.
// Code of a workflow must use cadence.Channel, cadence.Selector, and cadence.Go instead of
// native channels, select and go.
type Workflow interface {
	Execute(ctx Context, input []byte) (result []byte, err Error)
}

// NewWorkflowDefinition creates a  WorkflowDefinition from a Workflow
func NewWorkflowDefinition(workflow Workflow) WorkflowDefinition {
	return &workflowDefinition{workflow: workflow}
}

type workflowDefinition struct {
	workflow   Workflow
	dispatcher dispatcher
}

type workflowResult struct {
	workflowResult []byte
	error          Error
}

type activityClient struct {
	dispatcher  dispatcher
	asyncClient asyncActivityClient
}

// errorImpl implements Error
type errorImpl struct {
	reason  string
	details []byte
}

func (e *errorImpl) Error() string {
	return e.reason
}

// Reason is from Error interface
func (e *errorImpl) Reason() string {
	return e.reason
}

// Details is from Error interface
func (e *errorImpl) Details() []byte {
	return e.details
}

func (d *workflowDefinition) Execute(env workflowEnvironment, input []byte) {
	ctx := WithValue(background, workflowEnvironmentContextKey, env)
	var resultPtr *workflowResult
	ctx = WithValue(ctx, workflowResultContextKey, &resultPtr)

	dispatcher := newDispatcher(ctx, func(ctx Context) {
		r := &workflowResult{}
		r.workflowResult, r.error = d.workflow.Execute(ctx, input)
		rpp := getWorkflowResultPointerPointer(ctx)
		*rpp = r

	})
	executeDispatcher(ctx, dispatcher)
}

func (d *workflowDefinition) StackTrace() string {
	return d.dispatcher.StackTrace()
}

// executeDispatcher executed coroutines in the calling thread and calls workflow completion callbacks
// if root workflow function returned
func executeDispatcher(ctx Context, dispatcher dispatcher) {
	panicErr := dispatcher.ExecuteUntilAllBlocked()
	if panicErr != nil {
		getWorkflowEnvironment(ctx).Complete(nil, NewError(panicErr.Error(), []byte(panicErr.StackTrace())))
		dispatcher.Close()
		return
	}
	rp := *getWorkflowResultPointerPointer(ctx)
	if rp == nil {
		// Result is not set, so workflow is still executing
		return
	}
	// Cannot cast nil values from interface{} to interface
	var err Error
	if rp.error != nil {
		err = rp.error.(Error)
	}
	getWorkflowEnvironment(ctx).Complete(rp.workflowResult, err)
	dispatcher.Close()
}

// ExecuteActivity requests activity execution in the context of a workflow.
func ExecuteActivity(ctx Context, parameters ExecuteActivityParameters) (result []byte, err Error) {
	channelName := fmt.Sprintf("\"activity %v\"", parameters.ActivityID)
	resultChannel := NewNamedBufferedChannel(ctx, channelName, 1)
	getWorkflowEnvironment(ctx).ExecuteActivity(parameters, func(r []byte, e Error) {
		result = r
		if e != nil {
			err = e.(Error)
		}
		ok := resultChannel.SendAsync(true)
		if !ok {
			panic("unexpected")
		}
		executeDispatcher(ctx, getDispatcher(ctx))
	})
	_, _ = resultChannel.Recv(ctx)
	return
}

// GetWorkflowInfo extracts info of a current workflow from a context.
func GetWorkflowInfo(ctx Context) *WorkflowInfo {
	return getWorkflowEnvironment(ctx).WorkflowInfo()
}

