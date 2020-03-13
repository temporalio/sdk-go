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

package internal

import (
	"context"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/uber-go/tally"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.uber.org/zap"
)

type (
	// WorkerOptions is used to configure a worker instance.
	// The current timeout resolution implementation is in seconds and uses math.Ceil(d.Seconds()) as the duration. But is
	// subjected to change in the future.
	WorkerOptions struct {

		// Optional: To set the host:port for this worker to connect to.
		// Use "dns:///" prefix to enable DNS based round robin.
		// default: localhost:7233
		HostPort string

		// Optional: To set the maximum concurrent activity executions this worker can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentActivityExecutionSize(1k)
		MaxConcurrentActivityExecutionSize int

		// Optional: Sets the rate limiting on number of activities that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value. Default: 100k
		WorkerActivitiesPerSecond float64

		// Optional: To set the maximum concurrent local activity executions this worker can have.
		// The zero value of this uses the default value.
		// default: 1k
		MaxConcurrentLocalActivityExecutionSize int

		// Optional: Sets the rate limiting on number of local activities that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your local activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value. Default: 100k
		WorkerLocalActivitiesPerSecond float64

		// Optional: Sets the rate limiting on number of activities that can be executed per second.
		// This is managed by the server and controls activities per second for your entire tasklist
		// whereas WorkerActivityTasksPerSecond controls activities only per worker.
		// Notice that the number is represented in float, so that you can set it to less than
		// 1 if needed. For example, set the number to 0.1 means you want your activity to be executed
		// once for every 10 seconds. This can be used to protect down stream services from flooding.
		// The zero value of this uses the default value. Default: 100k
		TaskListActivitiesPerSecond float64

		// Optional: To set the maximum concurrent decision task executions this worker can have.
		// The zero value of this uses the default value.
		// default: defaultMaxConcurrentTaskExecutionSize(1k)
		MaxConcurrentDecisionTaskExecutionSize int

		// Optional: Sets the rate limiting on number of decision tasks that can be executed per second per
		// worker. This can be used to limit resources used by the worker.
		// The zero value of this uses the default value. Default: 100k
		WorkerDecisionTasksPerSecond float64

		// Optional: if the activities need auto heart beating for those activities
		// by the framework
		// default: false not to heartbeat.
		AutoHeartBeat bool

		// Optional: Sets an identify that can be used to track this host for debugging.
		// default: default identity that include hostname, groupName and process ID.
		Identity string

		// Optional: Metrics to be reported. Metrics emitted by the temporal client are not prometheus compatible by
		// default. To ensure metrics are compatible with prometheus make sure to create tally scope with sanitizer
		// options set.
		// var (
		// _safeCharacters = []rune{'_'}
		// _sanitizeOptions = tally.SanitizeOptions{
		// 	NameCharacters: tally.ValidCharacters{
		// 		Ranges:     tally.AlphanumericRange,
		// 		Characters: _safeCharacters,
		// 	},
		// 		KeyCharacters: tally.ValidCharacters{
		// 			Ranges:     tally.AlphanumericRange,
		// 			Characters: _safeCharacters,
		// 		},
		// 		ValueCharacters: tally.ValidCharacters{
		// 			Ranges:     tally.AlphanumericRange,
		// 			Characters: _safeCharacters,
		// 		},
		// 		ReplacementCharacter: tally.DefaultReplacementCharacter,
		// 	}
		// )
		// opts := tally.ScopeOptions{
		// 	Reporter:        reporter,
		// 	SanitizeOptions: &_sanitizeOptions,
		// }
		// scope, _ := tally.NewRootScope(opts, time.Second)
		// default: no metrics.
		MetricsScope tally.Scope

		// Optional: Logger framework can use to log.
		// default: default logger provided.
		Logger *zap.Logger

		// Optional: Enable logging in replay.
		// In the workflow code you can use workflow.GetLogger(ctx) to write logs. By default, the logger will skip log
		// entry during replay mode so you won't see duplicate logs. This option will enable the logging in replay mode.
		// This is only useful for debugging purpose.
		// default: false
		EnableLoggingInReplay bool

		// Optional: Disable running workflow workers.
		// default: false
		DisableWorkflowWorker bool

		// Optional: Disable running activity workers.
		// default: false
		DisableActivityWorker bool

		// Optional: Disable sticky execution.
		// default: false
		// Sticky Execution is to run the decision tasks for one workflow execution on same worker host. This is an
		// optimization for workflow execution. When sticky execution is enabled, worker keeps the workflow state in
		// memory. New decision task contains the new history events will be dispatched to the same worker. If this
		// worker crashes, the sticky decision task will timeout after StickyScheduleToStartTimeout, and temporal server
		// will clear the stickiness for that workflow execution and automatically reschedule a new decision task that
		// is available for any worker to pick up and resume the progress.
		DisableStickyExecution bool

		// Optional: Sticky schedule to start timeout.
		// default: 5s
		// The resolution is seconds. See details about StickyExecution on the comments for DisableStickyExecution.
		StickyScheduleToStartTimeout time.Duration

		// Optional: sets context for activity. The context can be used to pass any configuration to activity
		// like common logger for all activities.
		BackgroundActivityContext context.Context

		// Optional: Sets how decision worker deals with non-deterministic history events
		// (presumably arising from non-deterministic workflow definitions or non-backward compatible workflow definition changes).
		// default: NonDeterministicWorkflowPolicyBlockWorkflow, which just logs error but reply nothing back to server
		NonDeterministicWorkflowPolicy NonDeterministicWorkflowPolicy

		// Optional: Sets DataConverter to customize serialization/deserialization of arguments in Temporal
		// default: defaultDataConverter, an combination of thriftEncoder and jsonEncoder
		DataConverter DataConverter

		// Optional: worker graceful shutdown timeout
		// default: 0s
		WorkerStopTimeout time.Duration

		// Optional: Enable running session workers.
		// Session workers is for activities within a session.
		// Enable this option to allow worker to process sessions.
		// default: false
		EnableSessionWorker bool

		// Uncomment this option when we support automatic restablish failed sessions.
		// Optional: The identifier of the resource consumed by sessions.
		// It's the user's responsibility to ensure there's only one worker using this resourceID.
		// For now, if user doesn't specify one, a new uuid will be used as the resourceID.
		// SessionResourceID string

		// Optional: Sets the maximum number of concurrently running sessions the resource support.
		// default: 1000
		MaxConcurrentSessionExecutionSize int

		// Optional: Specifies factories used to instantiate workflow interceptor chain
		// The chain is instantiated per each replay of a workflow execution
		WorkflowInterceptorChainFactories []WorkflowInterceptorFactory

		// Optional: Sets ContextPropagators that allows users to control the context information passed through a workflow
		// default: no ContextPropagators
		ContextPropagators []ContextPropagator

		// Optional: Sets opentracing Tracer that is to be used to emit tracing information
		// default: no tracer - opentracing.NoopTracer
		Tracer opentracing.Tracer

		// Optional: Sets GRPCDialer that can be used to create gRPC connection
		// GRPCDialer must add params.RequiredInterceptors and set params.DefaultServiceConfig if round robin load balancer needs to be enabled:
		// func customGRPCDialer(params GRPCDialerParams) (*grpc.ClientConn, error) {
		//	return grpc.Dial(params.HostPort,
		//		grpc.WithInsecure(),                                            // Replace this with required transport security if needed
		//		grpc.WithChainUnaryInterceptor(params.RequiredInterceptors...), // Add custom interceptors here but params.RequiredInterceptors must be added anyway.
		//		grpc.WithDefaultServiceConfig(params.DefaultServiceConfig),     // DefaultServiceConfig enables round robin. Any valid gRPC service config can be used here (https://github.com/grpc/grpc/blob/master/doc/service_config.md).
		//	)
		// }
		// default: defaultGRPCDialer (same as above)
		GRPCDialer GRPCDialer
	}
)

// NonDeterministicWorkflowPolicy is an enum for configuring how client's decision task handler deals with
// mismatched history events (presumably arising from non-deterministic workflow definitions).
type NonDeterministicWorkflowPolicy int

const (
	// NonDeterministicWorkflowPolicyBlockWorkflow is the default policy for handling detected non-determinism.
	// This option simply logs to console with an error message that non-determinism is detected, but
	// does *NOT* reply anything back to the server.
	// It is chosen as default for backward compatibility reasons because it preserves the old behavior
	// for handling non-determinism that we had before NonDeterministicWorkflowPolicy type was added to
	// allow more configurability.
	NonDeterministicWorkflowPolicyBlockWorkflow NonDeterministicWorkflowPolicy = iota
	// NonDeterministicWorkflowPolicyFailWorkflow behaves exactly the same as Ignore, up until the very
	// end of processing a decision task.
	// Whereas default does *NOT* reply anything back to the server, fail workflow replies back with a request
	// to fail the workflow execution.
	NonDeterministicWorkflowPolicyFailWorkflow

	// ReplayDomainName is domainName for replay because startEvent doesn't contain it
	ReplayDomainName = "ReplayDomain"
)

// IsReplayDomain checks if the domainName is from replay
func IsReplayDomain(dn string) bool {
	return ReplayDomainName == dn
}

// NewWorker creates an instance of worker for managing workflow and activity executions.
// domain - the name of the temporal domain.
// taskList 	- is the task list name you use to identify your client worker, also
// 		  identifies group of workflow and activity implementations that are hosted by a single worker process.
// options 	-  configure any worker specific options like hostPort, logger, metrics, identity.
func NewWorker(
	domain string,
	taskList string,
	options WorkerOptions,
) (*AggregatedWorker, error) {

	if options.MetricsScope == nil {
		options.MetricsScope = tally.NoopScope
		options.Logger.Info("No metrics scope configured for temporal worker. Use NoopScope as default.")
	}

	metricsScope := tagScope(options.MetricsScope, tagDomain, domain, tagTaskList, taskList, clientImplHeaderName, clientImplHeaderValue)

	if len(options.HostPort) == 0 {
		options.HostPort = LocalHostPort
	}

	if options.GRPCDialer == nil {
		options.GRPCDialer = defaultGRPCDialer
	}

	connection, err := options.GRPCDialer(GRPCDialerParams{
		HostPort:             options.HostPort,
		RequiredInterceptors: requiredInterceptors(metricsScope),
		DefaultServiceConfig: DefaultServiceConfig,
	})

	if err != nil {
		return nil, err
	}

	return NewAggregatedWorker(workflowservice.NewWorkflowServiceClient(connection), connection, domain, taskList, options), nil
}
