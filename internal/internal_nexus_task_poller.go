package internal

import (
	"context"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/log"
)

type nexusTaskPoller struct {
	basePoller
	namespace       string
	taskQueueName   string
	identity        string
	service         workflowservice.WorkflowServiceClient
	taskHandler     *nexusTaskHandler
	logger          log.Logger
	numPollerMetric *numPollerMetric
}

var _ taskPoller = &nexusTaskPoller{}

func newNexusTaskPoller(
	taskHandler *nexusTaskHandler,
	service workflowservice.WorkflowServiceClient,
	params workerExecutionParameters,
) *nexusTaskPoller {
	return &nexusTaskPoller{
		basePoller: basePoller{
			metricsHandler:       params.MetricsHandler,
			stopC:                params.WorkerStopChannel,
			workerBuildID:        params.getBuildID(),
			useBuildIDVersioning: params.UseBuildIDForVersioning,
			capabilities:         params.capabilities,
		},
		taskHandler:     taskHandler,
		service:         service,
		namespace:       params.Namespace,
		taskQueueName:   params.TaskQueue,
		identity:        params.Identity,
		logger:          params.Logger,
		numPollerMetric: newNumPollerMetric(params.MetricsHandler, metrics.PollerTypeNexusTask),
	}
}

// Poll the nexus task queue and update the num_poller metric
func (ntp *nexusTaskPoller) pollNexusTaskQueue(ctx context.Context, request *workflowservice.PollNexusTaskQueueRequest) (*workflowservice.PollNexusTaskQueueResponse, error) {
	ntp.numPollerMetric.increment()
	defer ntp.numPollerMetric.decrement()

	return ntp.service.PollNexusTaskQueue(ctx, request)
}

func (ntp *nexusTaskPoller) poll(ctx context.Context) (interface{}, error) {
	traceLog(func() {
		ntp.logger.Debug("nexusTaskPoller::Poll")
	})
	request := &workflowservice.PollNexusTaskQueueRequest{
		Namespace: ntp.namespace,
		TaskQueue: &taskqueuepb.TaskQueue{Name: ntp.taskQueueName, Kind: enumspb.TASK_QUEUE_KIND_NORMAL},
		Identity:  ntp.identity,
		WorkerVersionCapabilities: &commonpb.WorkerVersionCapabilities{
			BuildId:       ntp.workerBuildID,
			UseVersioning: ntp.useBuildIDVersioning,
		},
	}

	response, err := ntp.pollNexusTaskQueue(ctx, request)
	if err != nil {
		return nil, err
	}
	if response == nil || len(response.TaskToken) == 0 {
		// No operation info is available on empty poll. Emit using base scope.
		ntp.metricsHandler.Counter(metrics.NexusPollNoTaskCounter).Inc(1)
		return nil, nil
	}

	return response, nil
}

// PollTask polls a new task
func (ntp *nexusTaskPoller) PollTask() (any, error) {
	return ntp.doPoll(ntp.poll)
}

// ProcessTask processes a new task
func (ntp *nexusTaskPoller) ProcessTask(task interface{}) error {
	if ntp.stopping() {
		return errStop
	}

	response := task.(*workflowservice.PollNexusTaskQueueResponse)
	if response.GetRequest() == nil {
		// We didn't get a request, poll must have timed out.
		traceLog(func() {
			ntp.logger.Debug("Empty Nexus poll response")
		})
		return nil
	}

	metricsHandler, handlerErr := ntp.taskHandler.metricsHandlerForTask(response)
	if handlerErr != nil {
		// context wasn't propagated to us, use a background context.
		_, err := ntp.taskHandler.client.WorkflowService().RespondNexusTaskFailed(
			context.Background(), ntp.taskHandler.fillInFailure(response.TaskToken, handlerErr))
		return err
	}

	executionStartTime := time.Now()

	// Schedule-to-start (from the time the request hit the frontend).
	scheduleToStartLatency := executionStartTime.Sub(response.GetRequest().GetScheduledTime().AsTime())
	metricsHandler.Timer(metrics.NexusTaskScheduleToStartLatency).Record(scheduleToStartLatency)

	// Process the nexus task.
	res, failure, err := ntp.taskHandler.Execute(response)

	// Execution latency (in-SDK processing time).
	metricsHandler.Timer(metrics.NexusTaskExecutionLatency).Record(time.Since(executionStartTime))
	if err != nil || failure != nil {
		metricsHandler.Counter(metrics.NexusTaskExecutionFailedCounter).Inc(1)
	}

	// Let the poller machinary drop the task, nothing to report back.
	// This is only expected due to context deadline errors.
	if err != nil {
		return err
	}

	if err := ntp.reportCompletion(res, failure); err != nil {
		traceLog(func() {
			ntp.logger.Debug("reportNexusTaskComplete failed", tagError, err)
		})
		return err
	}

	// E2E latency, from frontend until we finished reporting completion.
	metricsHandler.
		Timer(metrics.NexusTaskEndToEndLatency).
		Record(time.Since(response.GetRequest().GetScheduledTime().AsTime()))
	return nil
}

func (ntp *nexusTaskPoller) reportCompletion(
	completion *workflowservice.RespondNexusTaskCompletedRequest,
	failure *workflowservice.RespondNexusTaskFailedRequest,
) error {
	ctx := context.Background()
	// No workflow or activity tags to report.
	// Task queue expected to be empty for Respond*Task... requests.
	rpcMetricsHandler := ntp.metricsHandler.WithTags(metrics.RPCTags(metrics.NoneTagValue, metrics.NoneTagValue, metrics.NoneTagValue))
	ctx, cancel := newGRPCContext(ctx, grpcMetricsHandler(rpcMetricsHandler),
		defaultGrpcRetryParameters(ctx))
	defer cancel()

	if failure != nil {
		_, err := ntp.taskHandler.client.WorkflowService().RespondNexusTaskFailed(ctx, failure)
		return err
	}
	_, err := ntp.taskHandler.client.WorkflowService().RespondNexusTaskCompleted(ctx, completion)
	return err
}
