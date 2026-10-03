// Start calls configuration and instrumentation hooks synchronously, then uses
// Stop's mutex for each SDK resource change. Stop cleans every constructed child
// it owns. Run and the caller wait for the complete Start call, including plugins.
package internal

import (
	"context"
	"fmt"

	"github.com/nexus-rpc/sdk-go/nexus"
	genworkflowservice "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/internal/common/metrics"
	"go.temporal.io/sdk/internal/common/util"
)

// After StartWorker plugins call their supplied next function, start initializes
// the SDK children in order. Caller hooks run outside the mutex; each resource
// change either finishes before Stop or returns ErrWorkerShutdown unchanged.
func (aw *AggregatedWorker) start() error {
	if err := aw.beginResourceInitialization(); err != nil {
		return err
	}
	// Metadata reads belong to the client. Run or the host retains that client
	// until this synchronous call returns, even when Stop has already finished.
	if err := initBinaryChecksum(); err != nil {
		return fmt.Errorf("failed to get executable checksum: %v", err)
	} else if err = aw.client.ensureInitialized(context.Background()); err != nil {
		return err
	}
	capabilities, err := aw.client.loadCapabilities(context.Background())
	if err != nil {
		return err
	}
	nsData, err := aw.client.loadNamespaceData(aw.executionParams.MetricsHandler)
	if err != nil {
		return err
	}
	if err := aw.applyStartupSettings(capabilities, nsData); err != nil {
		return err
	}

	// Poller configuration and individual launches use separate admission steps.
	// MaxSlots, startup counters, and verbose logs remain synchronous outside them.
	if !util.IsInterfaceNil(aw.workflowWorker) {
		maxSlots := aw.workflowWorker.worker.slotSupplier.inner.MaxSlots()
		if err := aw.initializeWorkflowPollers(maxSlots); err != nil {
			return err
		}
		if err := aw.startBaseWorker(aw.workflowWorker.localActivityWorker); err != nil {
			return err
		}
		if err := aw.startBaseWorker(aw.workflowWorker.worker); err != nil {
			return err
		}
		if aw.client.eagerDispatcher != nil {
			if err := aw.registerEagerWorker(); err != nil {
				return err
			}
		}
	}
	if !util.IsInterfaceNil(aw.activityWorker) {
		if err := aw.initializeActivityPollers(); err != nil {
			return err
		}
		if err := aw.startBaseWorker(aw.activityWorker.worker); err != nil {
			return err
		}
	}
	if !util.IsInterfaceNil(aw.sessionWorker) && len(aw.registry.getRegisteredActivities()) > 0 {
		aw.logger.Info("Starting session worker")
		if err := aw.initializeSessionPollers(); err != nil {
			return err
		}
		if err := aw.startBaseWorker(aw.sessionWorker.creationWorker.worker); err != nil {
			return err
		}
		if err := aw.startBaseWorker(aw.sessionWorker.activityWorker.worker); err != nil {
			return err
		}
	}

	// After earlier children start, validate Nexus services and call their hooks.
	// These hooks allocate no SDK contexts. Construct and store the complete
	// Nexus child before its startup counter can call Stop.
	nexusServices := aw.registry.getRegisteredNexusServices()
	if len(nexusServices) > 0 {
		reg := nexus.NewServiceRegistry()
		for _, service := range nexusServices {
			if err := reg.Register(service); err != nil {
				return fmt.Errorf("failed to create a nexus worker: %w", err)
			}
		}
		reg.Use(nexusMiddleware(aw.registry.interceptors))
		handler, err := reg.NewHandler()
		if err != nil {
			return fmt.Errorf("failed to create a nexus worker: %w", err)
		}
		opts := prepareNexusWorker(nexusWorkerOptions{
			executionParameters: aw.executionParams,
			client:              aw.client,
			workflowService:     aw.client.workflowService,
			handler:             handler,
			registry:            aw.registry,
		})
		if err := aw.publishNexusWorker(opts); err != nil {
			return fmt.Errorf("failed to create a nexus worker: %w", err)
		}
		if err := aw.startBaseWorker(aw.nexusWorker.worker); err != nil {
			return fmt.Errorf("failed to start a nexus worker: %w", err)
		}
	}
	if aw.client.workerHeartbeatInterval > 0 {
		if err := aw.registerHeartbeatWorker(nsData); err != nil {
			return fmt.Errorf("failed to register heartbeat worker: %w", err)
		}
	}
	// After all required resources are initialized, the final logger can call
	// Stop and return. Startup still returns nil because initialization finished.
	aw.logger.Info("Started Worker")
	return nil
}

// A public Start call accepts startup before releasing the mutex for plugins.
// Calling Start after Stop has rejected further startup keeps the existing panic.
func (aw *AggregatedWorker) admitStart() {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	aw.assertNotStopped()
	aw.startAttempted = true
}

// Run accepts startup or waits for an already accepted startup call. If Stop ran
// before any attempt, return ErrWorkerShutdown without invoking plugins or
// creating resources; Run still waits for the existing cleanup to finish.
func (aw *AggregatedWorker) admitRun() error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	if !aw.startAttempted {
		select {
		case <-aw.stopC:
			return ErrWorkerShutdown
		default:
		}
		aw.startAttempted = true
	}
	return nil
}

// When plugins call next, beginResourceInitialization closes handler registration
// unless Stop has already prevented startup. A plugin error before next skips it.
func (aw *AggregatedWorker) beginResourceInitialization() error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.started.Store(true)
	return nil
}

// Successful metadata supplies the SDK settings before any poller uses them.
// applyStartupSettings invokes no caller hook; if Stop won, it returns
// ErrWorkerShutdown without writing those settings.
func (aw *AggregatedWorker) applyStartupSettings(capabilities *genworkflowservice.GetSystemInfoResponse_Capabilities, nsData namespaceData) error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	proto.Merge(aw.capabilities, capabilities)

	// Seed poller groups before the first poll.
	aw.executionParams.pollerGroupSnapshotStore.updateGroups(nsData.pollerGroupsInfo)
	if aw.sessionWorker != nil {
		aw.sessionWorker.activityWorker.executionParameters.pollerGroupSnapshotStore.updateGroups(nsData.pollerGroupsInfo)
	}

	if aw.executionParams.setErrorLimits != nil {
		payloadSizeError := int64(0)
		if nsData.limits.BlobSizeLimitError > 0 {
			payloadSizeError = nsData.limits.BlobSizeLimitError
		}
		memoSizeError := int64(0)
		if nsData.limits.MemoSizeLimitError > 0 {
			memoSizeError = nsData.limits.MemoSizeLimitError
		}
		aw.executionParams.setErrorLimits(&payloadLimits{
			payloadSize: payloadSizeError,
			memoSize:    memoSizeError,
		})
	}

	if nsData.capabilities.GetWorkerPollCompleteOnShutdown() {
		aw.workerPollCompleteOnShutdown.Store(true)
	}

	if nsData.capabilities.GetWorkflowTaskCompletionPagination() {
		aw.executionParams.workflowTaskCompletionPagination.enabled.Store(true)
		aw.executionParams.workflowTaskCompletionPagination.sizeLimit.Store(nsData.limits.GetWorkflowTaskCompletionSizeLimitError())
	}

	if nsData.capabilities.GetPollerAutoscaling() {
		aw.executionParams.serverSupportsAutoscaling.Store(true)
	}

	// If the namespace opts workers into poller autoscaling, auto-enroll any
	// poller type that was left at its default (the user set neither a fixed
	// poller count nor a poller behavior). Auto-enroll implies full autoscaling
	// support, including scaling down, so it also enables serverSupportsAutoscaling.
	if nsData.capabilities.GetPollerAutoscalingAutoEnroll() {
		aw.executionParams.serverSupportsAutoscaling.Store(true)
		autoscaling := NewPollerBehaviorAutoscaling(PollerBehaviorAutoscalingOptions{})
		if aw.executionParams.pollerAutoEnrollEligibility.nexusTask {
			aw.executionParams.NexusTaskPollerBehavior = autoscaling
		}
		if aw.executionParams.pollerAutoEnrollEligibility.workflowTask && !util.IsInterfaceNil(aw.workflowWorker) {
			aw.executionParams.WorkflowTaskPollerBehavior = autoscaling
		}
		if aw.executionParams.pollerAutoEnrollEligibility.activityTask {
			if !util.IsInterfaceNil(aw.activityWorker) {
				aw.executionParams.ActivityTaskPollerBehavior = autoscaling
			}
		}
	}

	return nil
}

// After MaxSlots returns, initializeWorkflowPollers constructs pollers from that
// saved value unless Stop won. It copies existing metric handles without invoking
// custom code and returns ErrWorkerShutdown if startup can no longer proceed.
func (aw *AggregatedWorker) initializeWorkflowPollers(maxSlots int) error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.workflowWorker.initializeTaskPollers(aw.executionParams.WorkflowTaskPollerBehavior, maxSlots)
	return nil
}

// initializeActivityPollers installs the activity child's SDK poller objects
// before shutdown seals admission, or returns without changing that child.
func (aw *AggregatedWorker) initializeActivityPollers() error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.activityWorker.initializeTaskPollers(aw.executionParams.ActivityTaskPollerBehavior)
	return nil
}

// After the session startup log returns, initializeSessionPollers installs both
// children's pollers unless Stop won. The creation child keeps its fixed behavior;
// no metrics or logger runs here, and rejection returns ErrWorkerShutdown.
func (aw *AggregatedWorker) initializeSessionPollers() error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.sessionWorker.activityWorker.initializeTaskPollers(aw.executionParams.ActivityTaskPollerBehavior)
	aw.sessionWorker.creationWorker.initializeTaskPollers(aw.sessionWorker.creationWorker.executionParameters.ActivityTaskPollerBehavior)
	return nil
}

// For one constructed child, startBaseWorker increments its startup counter,
// launches its SDK groups, then writes its verbose log. Counter and logger calls
// can Stop synchronously; only launch uses the mutex and can return shutdown.
func (aw *AggregatedWorker) startBaseWorker(bw *baseWorker) error {
	bw.metricsHandler.Counter(metrics.WorkerStartCounter).Inc(1)
	if err := aw.launchBaseWorker(bw); err != nil {
		return err
	}
	bw.logStarted()
	return nil
}

// For one child, launchBaseWorker starts every polling and dispatch group before
// releasing the mutex. Stop then sees a complete launch, or this method returns
// ErrWorkerShutdown without starting any group.
func (aw *AggregatedWorker) launchBaseWorker(bw *baseWorker) error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	bw.startPolling()
	return nil
}

// After the workflow startup log returns, registerEagerWorker adds it to the
// eager dispatcher map unless Stop won. A logger-triggered Stop therefore gets
// ErrWorkerShutdown instead of a later map insertion.
func (aw *AggregatedWorker) registerEagerWorker() error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.client.eagerDispatcher.registerWorker(aw.workflowWorker)
	return nil
}

// After Nexus hooks return, publishNexusWorker constructs and stores the complete
// child while holding Stop's mutex. Stop owns its cleanup after assignment;
// rejection returns ErrWorkerShutdown without allocating worker resources.
func (aw *AggregatedWorker) publishNexusWorker(opts nexusWorkerOptions) error {
	aw.lifecycleMu.Lock()
	defer aw.lifecycleMu.Unlock()
	select {
	case <-aw.stopC:
		return ErrWorkerShutdown
	default:
	}
	aw.nexusWorker = newNexusWorker(opts)
	return nil
}
