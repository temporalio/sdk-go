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
Package temporal and its subdirectories contain the Temporal client side framework.

The Temporal service is a task orchestrator for your application’s tasks. Applications using Temporal can execute a
logical flow of tasks, especially long-running business logic, asynchronously or synchronously. They can also scale at
runtime on distributed systems.

A quick example illustrates its use case. Consider Uber Eats where Temporal manages the entire business flow from
placing an order, accepting it, handling shopping cart processes (adding, updating, and calculating cart items),
entering the order in a pipeline (for preparing food and coordinating delivery), to scheduling delivery as well as
handling payments.

Temporal consists of a programming framework (or client library) and a managed service (or backend). The framework
enables developers to author and coordinate tasks in Go code.

The root temporal package contains common data structures. The subpackages are:

  - workflow - functions used to implement workflows
  - activity - functions used to implement activities
  - client - functions used to create Temporal service client used to start and
    monitor workflow executions.
  - worker - functions used to create worker instance used to host workflow and
    activity code.
  - testsuite - unit testing framework for activity and workflow testing

How Temporal works

The Temporal hosted service brokers and persists events generated during workflow execution. Worker nodes owned and
operated by customers execute the coordination and task logic. To facilitate the implementation of worker nodes Temporal
provides a client-side library for the Go language.

In Temporal, you can code the logical flow of events separately as a workflow and code business logic as activities. The
workflow identifies the activities and sequences them, while an activity executes the logic.

Key Features

Dynamic workflow execution graphs - Determine the workflow execution graphs at runtime based on the data you are
processing. Temporal does not pre-compute the execution graphs at compile time or at workflow start time. Therefore, you
have the ability to write workflows that can dynamically adjust to the amount of data they are processing. If you need
to trigger 10 instances of an activity to efficiently process all the data in one run, but only 3 for a subsequent run,
you can do that.

Child Workflows - Orchestrate the execution of a workflow from within another workflow. Temporal will return the results
of the child workflow execution to the parent workflow upon completion of the child workflow. No polling is required in
the parent workflow to monitor status of the child workflow, making the process efficient and fault tolerant.

Durable Timers - Implement delayed execution of tasks in your workflows that are robust to worker failures. Temporal
provides two easy to use APIs, **workflow.Sleep** and **workflow.Timer**, for implementing time based events in your
workflows. Temporal ensures that the timer settings are persisted and the events are generated even if workers executing
the workflow crash.

Signals - Modify/influence the execution path of a running workflow by pushing additional data directly to the workflow
using a signal. Via the Signal facility, Temporal provides a mechanism to consume external events directly in workflow
code.

Task routing - Efficiently process large amounts of data using a Temporal workflow, by caching the data locally on a
worker and executing all activities meant to process that data on that same worker. Temporal enables you to choose the
worker you want to execute a certain activity by scheduling that activity execution in the worker's specific task queue.

Unique workflow ID enforcement - Use business entity IDs for your workflows and let Temporal ensure that only one
workflow is running for a particular entity at a time. Temporal implements an atomic "uniqueness check" and ensures that
no race conditions are possible that would result in multiple workflow executions for the same workflow ID. Therefore,
you can implement your code to attempt to start a workflow without checking if the ID is already in use, even in the
cases where only one active execution per workflow ID is desired.

Perpetual/ContinueAsNew workflows - Run periodic tasks as a single perpetually running workflow. With the
"ContinueAsNew" facility, Temporal allows you to leverage the "unique workflow ID enforcement" feature for periodic
workflows. Temporal will complete the current execution and start the new execution atomically, ensuring you get to
keep your workflow ID. By starting a new execution Temporal also ensures that workflow execution history does not grow
indefinitely for perpetual workflows.

At-most once activity execution - Execute non-idempotent activities as part of your workflows. Temporal will not
automatically retry activities on failure. For every activity execution Temporal will return a success result, a failure
result, or a timeout to the workflow code and let the workflow code determine how each one of those result types should
be handled.

Asynch Activity Completion - Incorporate human input or thrid-party service asynchronous callbacks into your workflows.
Temporal allows a workflow to pause execution on an activity and wait for an external actor to resume it with a
callback. During this pause the activity does not have any actively executing code, such as a polling loop, and is
merely an entry in the Temporal datastore. Therefore, the workflow is unaffected by any worker failures happening over
the duration of the pause.

Activity Heartbeating - Detect unexpected failures/crashes and track progress in long running activities early. By
configuring your activity to report progress periodically to the Temporal server, you can detect a crash that occurs 10
minutes into an hour-long activity execution much sooner, instead of waiting for the 60-minute execution timeout. The
recorded progress before the crash gives you sufficient information to determine whether to restart the activity from
the beginning or resume it from the point of failure.

Timeouts for activities and workflow executions - Protect against stuck and unresponsive activities and workflows with
appropriate timeout values. Temporal requires that timeout values are provided for every activity or workflow
invocation. There is no upper bound on the timeout values, so you can set timeouts that span days, weeks, or even
months.

Visibility - Get a list of all your active and/or completed workflow. Explore the execution history of a particular
workflow execution. Temporal provides a set of visibility APIs that allow you, the workflow owner, to monitor past and
current workflow executions.

Debuggability - Replay any workflow execution history locally under a debugger. The Temporal client library provides an
API to allow you to capture a stack trace from any failed workflow execution history.
*/
package temporal
