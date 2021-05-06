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

/*
Package workflow contains functions and types used to implement Temporal workflows.

A workflow is an implementation of coordination logic. The Temporal programming framework (aka SDK) allows
you to write the workflow coordination logic as simple procedural code that uses standard Go data modeling. The client
library takes care of the communication between the worker service and the Temporal service, and ensures state
persistence between events even in case of worker failures. Any particular execution is not tied to a
particular worker machine. Different steps of the coordination logic can end up executing on different worker
instances, with the framework ensuring that necessary state is recreated on the worker executing the step.

In order to facilitate this operational model both the Temporal programming framework and the managed service impose
some requirements and restrictions on the implementation of the coordination logic. The details of these requirements
and restrictions are described in the "Implementation" section below.

Overview

The sample code below shows a simple implementation of a workflow that executes one activity. The workflow also passes
the sole parameter it receives as part of its initialization as a parameter to the activity.

	package sample

	import (
		"time"

		"go.temporal.io/sdk/workflow"
	)

	func SimpleWorkflow(ctx workflow.Context, value string) error {
		ao := workflow.ActivityOptions{
			TaskQueue:               "sampleTaskQueue",
			ScheduleToCloseTimeout: time.Second * 60,
			ScheduleToStartTimeout: time.Second * 60,
			StartToCloseTimeout:    time.Second * 60,
			HeartbeatTimeout:       time.Second * 10,
			WaitForCancellation:    false,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)

		future := workflow.ExecuteActivity(ctx, SimpleActivity, value)
		var result string
		if err := future.Get(ctx, &result); err != nil {
			return err
		}
		workflow.GetLogger(ctx).Info(“Done”, “result”, result)
		return nil
	}

The following sections describe what is going on in the above code.

Declaration

In the Temporal programing model a workflow is implemented with a function. The function declaration specifies the
parameters the workflow accepts as well as any values it might return.

	func SimpleWorkflow(ctx workflow.Context, value string) error

The first parameter to the function is ctx workflow.Context. This is a required parameter for all workflow functions
and is used by the Temporal client library to pass execution context. Virtually all the client library functions that
are callable from the workflow functions require this ctx parameter. This **context** parameter is the same concept as
the standard context.Context provided by Go. The only difference between workflow.Context and context.Context is that
the Done() function in workflow.Context returns workflow.Channel instead of the standard go chan.

The second string parameter is a custom workflow parameter that can be used to pass in data into the workflow on start.
A workflow can have one or more such parameters. All parameters to an workflow function must be serializable, which
essentially means that params can’t be channels, functions, variadic, or unsafe pointer.

Since it only declares error as the return value it means that the workflow does not return a value. The error return
value is used to indicate an error was encountered during execution and the workflow should be terminated.

Implementation

In order to support the synchronous and sequential programming model for the workflow implementation there are certain
restrictions and requirements on how the workflow implementation must behave in order to guarantee correctness. The
requirements are that:

  - Execution must be deterministic
  - Execution must be idempotent

A simplistic way to think about these requirements is that the workflow code:

  - Can only read and manipulate local state or state received as return values
    from Temporal client library functions
  - Should really not affect changes in external systems other than through
    invocation of activities
  - Should interact with time only through the functions provided by the
    Temporal client library (i.e. workflow.Now(), workflow.Sleep())
  - Should not create and interact with goroutines directly, it should instead
    use the functions provided by the Temporal client library. (i.e.
    workflow.Go() instead of go, workflow.Channel instead of chan,
    workflow.Selector instead of select)
  - Should do all logging via the logger provided by the Temporal client
    library (i.e. workflow.GetLogger())
  - Should not iterate over maps using range as order of map iteration is
    randomized

Now that we laid out the ground rules we can take a look at how to implement some common patterns inside workflows.

Special Temporal client library functions and types

The Temporal client library provides a number of functions and types as alternatives to some native Go functions and
types. Usage of these replacement functions/types is necessary in order to ensure that the workflow code execution is
deterministic and repeatable within an execution context.

Coroutine related constructs:

  - workflow.Go : This is a replacement for the the go statement
  - workflow.Channel : This is a replacement for the native chan type. Temporal
    provides support for both buffered and unbuffered channels
  - workflow.Selector : This is a replacement for the select statement

Time related functions:

  - workflow.Now() : This is a replacement for time.Now()
  - workflow.Sleep() : This is a replacement for time.Sleep()

Failing a Workflow

To mark a workflow as failed all that needs to happen is for the workflow function to return an error via the err
return value.

Execute Activity

The primary responsibility of the workflow implementation is to schedule activities for execution. The most
straightforward way to do that is via the library method workflow.ExecuteActivity:

	ao := workflow.ActivityOptions{
		TaskQueue:               "sampleTaskQueue",
		ScheduleToCloseTimeout: time.Second * 60,
		ScheduleToStartTimeout: time.Second * 60,
		StartToCloseTimeout:    time.Second * 60,
		HeartbeatTimeout:       time.Second * 10,
		WaitForCancellation:    false,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	future := workflow.ExecuteActivity(ctx, SimpleActivity, value)
	var result string
	if err := future.Get(ctx, &result); err != nil {
		return err
	}

Before calling workflow.ExecuteActivity(), ActivityOptions must be configured for the invocation. These are for the
most part options to customize various execution timeouts. These options are passed in by creating a child context from
the initial context and overwriting the desired values. The child context is then passed into the
workflow.ExecuteActivity() call. If multiple activities are sharing the same exact option values then the same context
instance can be used when calling workflow.ExecuteActivity().

The first parameter to the call is the required workflow.Context object. This type is an exact copy of context.Context
with the Done() method returning workflow.Channel instead of native go chan.

The second parameter is the function that we registered as an activity function. This parameter can also be the a
string representing the fully qualified name of the activity function. The benefit of passing in the actual function
object is that in that case the framework can validate activity parameters.

The remaining parameters are the parameters to pass to the activity as part of the call. In our example we have a
single parameter: **value**. This list of parameters must match the list of parameters declared by the activity
function. Like mentioned above the Temporal client library will validate that this is indeed the case.

The method call returns immediately and returns a workflow.Future. This allows for more code to be executed without
having to wait for the scheduled activity to complete.

When we are ready to process the results of the activity we call the Get() method on the future object returned. The
parameters to this method are the ctx object we passed to the workflow.ExecuteActivity() call and an output parameter
that will receive the output of the activity. The type of the output parameter must match the type of the return value
declared by the activity function. The Get() method will block until the activity completes and results are available.

The result value returned by workflow.ExecuteActivity() can be retrieved from the future and used like any normal
result from a synchronous function call. If the result above is a string value we could use it as follows:

	var result string
	if err := future.Get(ctx1, &result); err != nil {
		return err
	}

	switch result {
	case “apple”:
		// do something
	case “bannana”:
		// do something
	default:
		return err
	}

In the example above we called the Get() method on the returned future immediately after workflow.ExecuteActivity().
However, this is not necessary. If we wish to execute multiple activities in parallel we can repeatedly call
workflow.ExecuteActivity() store the futures returned and then wait for all activities to complete by calling the
Get() methods of the future at a later time.

To implement more complex wait conditions on the returned future objects, use the workflow.Selector class. Take a look
at our Pickfirst sample for an example of how to use of workflow.Selector.

Child Workflow

workflow.ExecuteChildWorkflow enables the scheduling of other workflows from within a workflow's implementation. The
parent workflow has the ability to "monitor" and impact the life-cycle of the child workflow in a similar way it can do
for an activity it invoked.

	cwo := workflow.ChildWorkflowOptions{
		// Do not specify WorkflowID if you want temporal to generate a unique ID for child execution
		WorkflowID:                   "BID-SIMPLE-CHILD-WORKFLOW",
		WorkflowExecutionTimeout: time.Minute * 30,
	}
	ctx = workflow.WithChildOptions(ctx, cwo)

	var result string
	future := workflow.ExecuteChildWorkflow(ctx, SimpleChildWorkflow, value)
	if err := future.Get(ctx, &result); err != nil {
		workflow.GetLogger(ctx).Error("SimpleChildWorkflow failed.", "Error", err)
		return err
	}

Before calling workflow.ExecuteChildWorkflow(), ChildWorkflowOptions must be configured for the invocation. These are
for the most part options to customize various execution timeouts. These options are passed in by creating a child
context from the initial context and overwriting the desired values. The child context is then passed into the
workflow.ExecuteChildWorkflow() call. If multiple activities are sharing the same exact option values then the same
context instance can be used when calling workflow.ExecuteChildWorkflow().

The first parameter to the call is the required workflow.Context object. This type is an exact copy of context.Context
with the Done() method returning workflow.Channel instead of the native go chan.

The second parameter is the function that we registered as a workflow function. This parameter can also be a string
representing the fully qualified name of the workflow function. What's the benefit? When you pass in the actual
function object, the framework can validate workflow parameters.

The remaining parameters are the parameters to pass to the workflow as part of the call. In our example we have a
single parameter: value. This list of parameters must match the list of parameters declared by the workflow function.

The method call returns immediately and returns a workflow.Future. This allows for more code to be executed without
having to wait for the scheduled workflow to complete.

When we are ready to process the results of the workflow we call the Get() method on the future object returned. The
parameters to this method are the ctx object we passed to the workflow.ExecuteChildWorkflow() call and an output
parameter that will receive the output of the workflow. The type of the output parameter must match the type of the
return value declared by the workflow function. The Get() method will block until the workflow completes and results
are available.

The workflow.ExecuteChildWorkflow() function is very similar to the workflow.ExecuteActivity() function. All the
patterns described for using the workflow.ExecuteActivity() apply to the workflow.ExecuteChildWorkflow() function as
well.

Child workflows can also be configured to continue to exist once their parent workflow is closed. When using this
pattern, extra care needs to be taken to ensure the child workflow is started before the parent workflow finishes.

	cwo := workflow.ChildWorkflowOptions{
		// Do not terminate when parent closes.
		// assumes import enumspb "go.temporal.io/api/enums/v1"
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	ctx = workflow.WithChildOptions(ctx, cwo)

	future := workflow.ExecuteChildWorkflow(ctx, SimpleChildWorkflow, value)

	// Wait for the child workflow to start
	if err := future.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		// Problem starting workflow.
		return err
	}

Error Handling

Activities and child workflows can fail. You could handle errors differently based on different error cases. If the
activity returns an error as errors.New() or fmt.Errorf(), those errors will be converted to error.GenericError. If the
activity returns an error as error.NewCustomError("err-reason", details), that error will be converted to
*error.CustomError. There are other types of errors like error.TimeoutError, error.CanceledError and error.PanicError.
So the error handling code would look like:

	err := workflow.ExecuteActivity(ctx, YourActivityFunc).Get(ctx, nil)
	switch err := err.(type) {
	case *error.CustomError:
		switch err.Reason() {
		case "err-reason-a":
			// handle error-reason-a
			var details YourErrorDetailsType
			err.Details(&details)
			// deal with details
		case "err-reason-b":
			// handle error-reason-b
		default:
			// handle all other error reasons
		}
	case *error.GenericError:
		switch err.Error() {
		case "err-msg-1":
			// handle error with message "err-msg-1"
		case "err-msg-2":
			// handle error with message "err-msg-2"
		default:
			// handle all other generic errors
		}
	case *error.TimeoutError:
		switch err.TimeoutType() {
		case shared.TimeoutTypeScheduleToStart:
			// handle ScheduleToStart timeout
		case shared.TimeoutTypeStartToClose:
			// handle StartToClose timeout
		case shared.TimeoutTypeHeartbeat:
			// handle heartbeat timeout
		default:
		}
	case *error.PanicError:
		 // handle panic error
	case *error.CanceledError:
		// handle canceled error
	default:
		// all other cases (ideally, this should not happen)
	}

Signals

Signals provide a mechanism to send data directly to a running workflow. Previously, you had two options for passing
data to the workflow implementation:

  - Via start parameters
  - As return values from activities

With start parameters, we could only pass in values before workflow execution begins.

Return values from activities allowed us to pass information to a running workflow, but this approach comes with its
own complications. One major drawback is reliance on polling. This means that the data needs to be stored in a
third-party location until it's ready to be picked up by the activity. Further, the lifecycle of this activity requires
management, and the activity requires manual restart if it fails before acquiring the data.

Signals, on the other hand, provides a fully asynch and durable mechanism for providing data to a running workflow.
When a signal is received for a running workflow, Temporal persists the event and the payload in the workflow history.
The workflow can then process the signal at any time afterwards without the risk of losing the information. The
workflow also has the option to stop execution by blocking on a signal channel.

	var signalVal string
	signalChan := workflow.GetSignalChannel(ctx, signalName)

	s := workflow.NewSelector(ctx)
	s.AddReceive(signalChan, func(c workflow.Channel, more bool) {
		c.Receive(ctx, &signalVal)
		workflow.GetLogger(ctx).Info("Received signal!", "signal", signalName, "value", signalVal)
	})
	s.Select(ctx)

	if len(signalVal) > 0 && signalVal != "SOME_VALUE" {
		return errors.New("signalVal")
	}

In the example above, the workflow code uses workflow.GetSignalChannel to open a workflow.Channel for the named signal.
We then use a workflow.Selector to wait on this channel and process the payload received with the signal.

ContinueAsNew Workflow Completion

Workflows that need to rerun periodically could naively be implemented as a big for loop with a sleep where the entire
logic of the workflow is inside the body of the for loop. The problem with this approach is that the history for that
workflow will keep growing to a point where it reaches the maximum size enforced by the service.

ContinueAsNew is the low level construct that enables implementing such workflows without the risk of failures down the
road. The operation atomically completes the current execution and starts a new execution of the workflow with the same
workflow ID. The new execution will not carry over any history from the old execution. To trigger this behavior, the
workflow function should terminate by returning the special ContinueAsNewError error:

	func SimpleWorkflow(workflow.Context ctx, value string) error {
	    ...
	    return workflow.NewContinueAsNewError(ctx, SimpleWorkflow, value)
	}

For a complete example implementing this pattern please refer to the Cron example.

SideEffect API

workflow.SideEffect executes the provided function once, records its result into the workflow history, and doesn't
re-execute upon replay. Instead, it returns the recorded result. Use it only for short, nondeterministic code snippets,
like getting a random value or generating a UUID. It can be seen as an "inline" activity. However, one thing to note
about workflow.SideEffect is that whereas for activities Temporal guarantees "at-most-once" execution, no such guarantee
exists for workflow.SideEffect. Under certain failure conditions, workflow.SideEffect can end up executing the function
more than once.

The only way to fail SideEffect is to panic, which causes workflow task failure. The workflow task after timeout is
rescheduled and re-executed giving SideEffect another chance to succeed. Be careful to not return any data from the
SideEffect function any other way than through its recorded return value.

	encodedRandom := SideEffect(func(ctx workflow.Context) interface{} {
		return rand.Intn(100)
	})

	var random int
	encodedRandom.Get(&random)
	if random < 50 {
		....
	} else {
		....
	}

Query API

A workflow execution could be stuck at some state for longer than expected period. Temporal provide facilities to query
the current call stack of a workflow execution. You can use tctl to do the query, for example:

	tctl --namespace samples-namespace workflow query -w my_workflow_id -r my_run_id -qt __stack_trace

The above cli command uses __stack_trace as the query type. The __stack_trace is a built-in query type that is
supported by temporal client library. You can also add your own custom query types to support thing like query current
state of the workflow, or query how many activities the workflow has completed. To do so, you need to setup your own
query handler using workflow.SetQueryHandler in your workflow code:

	func MyWorkflow(ctx workflow.Context, input string) error {
	   currentState := "started" // this could be any serializable struct
	   err := workflow.SetQueryHandler(ctx, "state", func() (string, error) {
		 return currentState, nil
	   })
	   if err != nil {
		 return err
	   }
	   // your normal workflow code begins here, and you update the currentState as the code makes progress.
	   currentState = "waiting timer"
	   err = NewTimer(ctx, time.Hour).Get(ctx, nil)
	   if err != nil {
		 currentState = "timer failed"
		 return err
	   }
	   currentState = "waiting activity"
	   ctx = WithActivityOptions(ctx, myActivityOptions)
	   err = ExecuteActivity(ctx, MyActivity, "my_input").Get(ctx, nil)
	   if err != nil {
		 currentState = "activity failed"
		 return err
	   }
	   currentState = "done"
	   return nil
	}

The above sample code sets up a query handler to handle query type "state". With that, you should be able to query with
cli:

	tctl --namespace samples-namespace workflow query -w my_workflow_id -r my_run_id -qt state

Besides using tctl, you can also issue query from code using QueryWorkflow() API on temporal Client object.

Registration

For some client code to be able to invoke a workflow type, the worker process needs to be aware of all the
implementations it has access to. A workflow is registered with the following call:

	worker.RegisterWorkflow(SimpleWorkflow)

This call essentially creates an in memory mapping inside the worker process between the fully qualified function name
and the implementation. If the worker receives tasks for a workflow type it does not know it will fail that task.
However, the failure of the task will not cause the entire workflow to fail.

Similarly, we need to have at least one worker that hosts the activity functions:

	worker.RegisterActivity(MyActivity)

See the activity package for more details on activity registration.

Testing

The Temporal client library provides a test framework to facilitate testing workflow implementations. The framework is
suited for implementing unit tests as well as functional tests of the workflow logic.

The code below implements the unit tests for the SimpleWorkflow sample.

	package sample

	import (
		"errors"
		"testing"

		"github.com/stretchr/testify/mock"
		"github.com/stretchr/testify/suite"

		"go.temporal.io/sdk/testsuite"
	)

	type UnitTestSuite struct {
		suite.Suite
		testsuite.WorkflowTestSuite

		env *testsuite.TestWorkflowEnvironment
	}

	func (s *UnitTestSuite) SetupTest() {
		s.env = s.NewTestWorkflowEnvironment()
	}

	func (s *UnitTestSuite) AfterTest(suiteName, testName string) {
		s.env.AssertExpectations(s.T())
	}

	func (s *UnitTestSuite) Test_SimpleWorkflow_Success() {
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_success")

		s.True(s.env.IsWorkflowCompleted())
		s.NoError(s.env.GetWorkflowError())
	}

	func (s *UnitTestSuite) Test_SimpleWorkflow_ActivityParamCorrect() {
		s.env.OnActivity(SimpleActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, value string) (string, error) {
			s.Equal("test_success", value)
			return value, nil
		})
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_success")

		s.True(s.env.IsWorkflowCompleted())
		s.NoError(s.env.GetWorkflowError())
	}

	func (s *UnitTestSuite) Test_SimpleWorkflow_ActivityFails() {
		s.env.OnActivity(SimpleActivity, mock.Anything, mock.Anything).Return("", errors.New("SimpleActivityFailure"))
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_failure")

		s.True(s.env.IsWorkflowCompleted())

		s.NotNil(s.env.GetWorkflowError())
		_, ok := s.env.GetWorkflowError().(*error.GenericError)
		s.True(ok)
		s.Equal("SimpleActivityFailure", s.env.GetWorkflowError().Error())
	}

	func TestUnitTestSuite(t *testing.T) {
		suite.Run(t, new(UnitTestSuite))
	}

Setup

First, we define a "test suite" struct that absorbs both the basic suite functionality from testify
http://godoc.org/github.com/stretchr/testify/suite via suite.Suite and the suite functionality from the Temporal test
framework via testsuite.WorkflowTestSuite. Since every test in this suite will test our workflow we add a property to
our struct to hold an instance of the test environment. This will allow us to initialize the test environment in a
setup method. For testing workflows we use a testsuite.TestWorkflowEnvironment.

We then implement a SetupTest method to setup a new test environment before each test. Doing so ensure that each test
runs in it's own isolated sandbox. We also implement an AfterTest function where we assert that all mocks we setup were
indeed called by invoking s.env.AssertExpectations(s.T()).

Finally, we create a regular test function recognized by "go test" and pass the struct to suite.Run.

A Simple Test

The simplest test case we can write is to have the test environment execute the workflow and then evaluate the results.

	func (s *UnitTestSuite) Test_SimpleWorkflow_Success() {
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_success")

		s.True(s.env.IsWorkflowCompleted())
		s.NoError(s.env.GetWorkflowError())
	}

Calling s.env.ExecuteWorkflow(...) will execute the workflow logic and any invoked activities inside the test process.
The first parameter to s.env.ExecuteWorkflow(...) is the workflow functions and any subsequent parameters are values
for custom input parameters declared by the workflow function. An important thing to note is that unless the activity
invocations are mocked or activity implementation replaced (see next section), the test environment will execute the
actual activity code including any calls to outside services.

In the example above, after executing the workflow we assert that the workflow ran through to completion via the call
to s.env.IsWorkflowComplete(). We also assert that no errors where returned by asserting on the return value of
s.env.GetWorkflowError(). If our workflow returned a value, we we can retrieve that value via a call to
s.env.GetWorkflowResult(&value) and add asserts on that value.

Activity Mocking and Overriding

When testing workflows, especially unit testing workflows, we want to test the workflow logic in isolation.
Additionally, we want to inject activity errors during our tests runs. The test framework provides two mechanisms that
support these scenarios: activity mocking and activity overriding. Both these mechanisms allow you to change the
behavior of activities invoked by your workflow without having to modify the actual workflow code.

Lets first take a look at a test that simulates a test failing via the "activity mocking" mechanism.

	func (s *UnitTestSuite) Test_SimpleWorkflow_ActivityFails() {
		s.env.OnActivity(SimpleActivity, mock.Anything, mock.Anything).Return("", errors.New("SimpleActivityFailure"))
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_failure")

		s.True(s.env.IsWorkflowCompleted())

		s.NotNil(s.env.GetWorkflowError())
		_, ok := s.env.GetWorkflowError().(*error.GenericError)
		s.True(ok)
		s.Equal("SimpleActivityFailure", s.env.GetWorkflowError().Error())
	}

In this test we want to simulate the execution of the activity SimpleActivity invoked by our workflow SimpleWorkflow
returning an error. We do that by setting up a mock on the test environment for the SimpleActivity that returns an
error.

	s.env.OnActivity(SimpleActivity, mock.Anything, mock.Anything).Return("", errors.New("SimpleActivityFailure"))

With the mock set up we can now execute the workflow via the s.env.ExecuteWorkflow(...) method and assert that the
workflow completed successfully and returned the expected error.

Simply mocking the execution to return a desired value or error is a pretty powerful mechanism to isolate workflow
logic. However, sometimes we want to replace the activity with an alternate implementation to support a more complex
test scenario. For our simple workflow lets assume we wanted to validate that the activity gets called with the
correct parameters.

	func (s *UnitTestSuite) Test_SimpleWorkflow_ActivityParamCorrect() {
		s.env.OnActivity(SimpleActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, value string) (string, error) {
			s.Equal("test_success", value)
			return value, nil
		})
		s.env.ExecuteWorkflow(SimpleWorkflow, "test_success")

		s.True(s.env.IsWorkflowCompleted())
		s.NoError(s.env.GetWorkflowError())
	}

In this example, we provide a function implementation as the parameter to Return. This allows us to provide an
alternate implementation for the activity SimpleActivity. The framework will execute this function whenever the
activity is invoked and pass on the return value from the function as the result of the activity invocation.
Additionally, the framework will validate that the signature of the "mock" function matches the signature of the
original activity function.

Since this can be an entire function, there really is no limitation as to what we can do in here. In this example, to
assert that the "value" param has the same content to the value param we passed to the workflow.
*/
package workflow
