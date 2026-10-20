package internal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/common/retry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// pollTimeSkippingTimeout bounds a single PollWorkflowExecutionTimeSkipping
// call. The server answers sooner with POLL_TIMEOUT under normal conditions;
// this is the client-side ceiling, after which the poll is simply reissued.
const pollTimeSkippingTimeout = 70 * time.Second

// TimeSkippingFastForwardOutcome is the result of waiting on a fast-forward.
//
// NOTE: Experimental
type TimeSkippingFastForwardOutcome int

const (
	// TimeSkippingFastForwardCompleted means the fast-forward reached its target.
	TimeSkippingFastForwardCompleted TimeSkippingFastForwardOutcome = iota
	// TimeSkippingFastForwardEnded means the fast-forward stopped without
	// reaching its target — the execution ended, time skipping was disabled, or
	// the configuration was reset.
	TimeSkippingFastForwardEnded
)

// PollWorkflowTimeSkipping waits for the fast-forward identified by
// fastForwardID to settle on the given workflow.
//
// It mirrors the long-poll handling used for workflow updates: a fresh
// long-poll context per call, cancelled after each, with a lapsed poll window
// reissued rather than surfaced. A superseding fast-forward is reported as an
// error, since the one being waited on can then never complete.
//
// NOTE: Experimental
func PollWorkflowTimeSkipping(
	ctx context.Context,
	svc workflowservice.WorkflowServiceClient,
	namespace, workflowID, fastForwardID string,
) (TimeSkippingFastForwardOutcome, error) {
	req := &workflowservice.PollWorkflowExecutionTimeSkippingRequest{
		Namespace: namespace,
		// RunId is left empty so the server resolves the current run, letting a
		// fast-forward follow a chain across retry / continue-as-new / cron.
		WorkflowExecution: &commonpb.WorkflowExecution{WorkflowId: workflowID},
		FastForwardId:     fastForwardID,
	}
	for {
		pollCtx, cancel := newGRPCContext(ctx, grpcLongPoll(true), grpcTimeout(pollTimeSkippingTimeout))
		pollCtx = context.WithValue(pollCtx, retry.ConfigKey,
			createDynamicServiceRetryPolicy(pollCtx).GrpcRetryConfig())
		resp, err := svc.PollWorkflowExecutionTimeSkipping(pollCtx, req)
		lapsed := pollCtx.Err() != nil
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				return TimeSkippingFastForwardEnded, ctx.Err()
			}
			// Our own poll window closed, either as a context error or as the
			// gRPC status it can surface as. Reissue rather than fail.
			if code := status.Code(err); lapsed || code == codes.DeadlineExceeded || code == codes.Canceled {
				continue
			}
			return TimeSkippingFastForwardEnded, err
		}

		switch resp.GetFastForwardPollingResult() {
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_COMPLETED:
			return TimeSkippingFastForwardCompleted, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_FAST_FORWARD_FAILED:
			// failed_reason distinguishes an id mismatch — meaning a later
			// fast-forward replaced this one, so it can never complete — from
			// benign endings such as the execution finishing first.
			if strings.Contains(resp.GetFailedReason(), "fast_forward_id does not match") {
				return TimeSkippingFastForwardEnded, fmt.Errorf(
					"fast_forward %q was replaced by a later fast_forward call (server: %s)",
					fastForwardID, resp.GetFailedReason())
			}
			return TimeSkippingFastForwardEnded, nil
		case enumspb.FAST_FORWARD_POLLING_RESULT_POLL_TIMEOUT:
			continue
		default:
			return TimeSkippingFastForwardEnded, fmt.Errorf(
				"PollWorkflowExecutionTimeSkipping returned unknown result %v",
				resp.GetFastForwardPollingResult())
		}
	}
}

// PatchScheduleTimeSkippingConfig applies a time-skipping configuration to a
// schedule, replacing any existing one.
//
// Time skipping is configured through SchedulePatch: the Schedule message
// carries no field for it, so UpdateSchedule can neither set nor clear it. This
// is the same mechanism Pause, Unpause, Trigger and Backfill use, each setting
// its own SchedulePatch field.
//
// NOTE: Experimental
func PatchScheduleTimeSkippingConfig(
	ctx context.Context,
	svc workflowservice.WorkflowServiceClient,
	namespace, scheduleID, identity string,
	config *commonpb.TimeSkippingConfig,
) error {
	grpcCtx, cancel := newGRPCContext(ctx, defaultGrpcRetryParameters(ctx))
	defer cancel()
	_, err := svc.PatchSchedule(grpcCtx, &workflowservice.PatchScheduleRequest{
		Namespace:  namespace,
		ScheduleId: scheduleID,
		Patch: &schedulepb.SchedulePatch{
			TimeSkippingConfig: config,
		},
		Identity:  identity,
		RequestId: uuid.NewString(),
	})
	return err
}

// TimeSkippingFastForwardConfigToProto builds a FastForwardConfig proto.
//
// NOTE: Experimental
func TimeSkippingFastForwardConfigToProto(id string, duration time.Duration) *commonpb.FastForwardConfig {
	return &commonpb.FastForwardConfig{
		Id:       id,
		Duration: durationpb.New(duration),
	}
}
