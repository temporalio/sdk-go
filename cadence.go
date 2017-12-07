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

// Package cadence and its subdirectories contain a Cadence client side framework.
//
// The Cadence service is a task orchestrator for your application’s tasks. Applications using Cadence can execute a logical flow of
// tasks, especially long-running business logic, asynchronously or synchronously. and can scale runtime on distributed
// systems without you, the service owner, worrying about infrastructure needs.
//
//A quick example illustrates its use case. Consider Uber Eats where Cadence manages the entire business flow from
// placing an order, accepting it, handling shopping cart processes (adding, updating, and calculating cart items),
// entering the order in a pipeline (for preparing food and coordinating delivery), to scheduling delivery as well as
// handling payments.
//
//Cadence consists of a programming framework (or client library) and a managed service (or backend). The framework
// enables developers to author and coordinate tasks in Go code.
//
// The root cadence package contains common data structures. The subpackages are:
//
// workflow - functions used to implement workflows
//
// activity - functions used to implement activities
//
// client - functions used to create Cadence service client used to start and monitor workflow executions.
//
// worker - functions used to create worker instance used to host workflow and activity code.
//
// testsuite - unit testing framework for activity and workflow testing.
package cadence
