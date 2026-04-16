package internal

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/proxy"
	workflowservice "go.temporal.io/api/workflowservice/v1"
)

// visitorFunc is a PayloadVisitor backed by a plain function, used in tests.
type visitorFunc func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error)

func (f visitorFunc) Visit(ctx *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return f(ctx, p)
}

// scheduleActivitiesRequest builds a RespondWorkflowTaskCompletedRequest with n
// ScheduleActivityTask commands, each carrying an Input Payloads field.
// proxy.VisitPayloads spawns one concurrent goroutine per *commonpb.Payloads
// field, giving us a controlled number of concurrent visitor calls.
func scheduleActivitiesRequest(n int) *workflowservice.RespondWorkflowTaskCompletedRequest {
	commands := make([]*commandpb.Command, n)
	for i := range n {
		commands[i] = &commandpb.Command{
			CommandType: enumspb.COMMAND_TYPE_SCHEDULE_ACTIVITY_TASK,
			Attributes: &commandpb.Command_ScheduleActivityTaskCommandAttributes{
				ScheduleActivityTaskCommandAttributes: &commandpb.ScheduleActivityTaskCommandAttributes{
					ActivityId:   fmt.Sprintf("activity-%d", i),
					ActivityType: &commonpb.ActivityType{Name: "test-activity"},
					Input:        &commonpb.Payloads{Payloads: []*commonpb.Payload{{Data: []byte("input")}}},
				},
			},
		}
	}
	return &workflowservice.RespondWorkflowTaskCompletedRequest{Commands: commands}
}

// TestVisitProtoPayloads_ConcurrentVisitors verifies that when concurrencyLimit
// equals the number of payload groups, all visitor calls overlap.
func TestVisitProtoPayloads_ConcurrentVisitors(t *testing.T) {
	const n = 4

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	arrived := make(chan struct{}, n)
	release := make(chan struct{})

	visitor := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
		select {
		case arrived <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return p, nil
	})

	go func() {
		for range n {
			select {
			case <-arrived:
			case <-ctx.Done():
				return
			}
		}
		close(release)
	}()

	require.NoError(t, visitProtoPayloads(ctx, visitor, scheduleActivitiesRequest(n), n))
}

// TestVisitProtoPayloads_SequentialVisitors verifies that concurrencyLimit=1
// prevents any overlap between visitor calls.
func TestVisitProtoPayloads_SequentialVisitors(t *testing.T) {
	const n = 4

	var current, peak int64
	visitor := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
		cur := atomic.AddInt64(&current, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		atomic.AddInt64(&current, -1)
		return p, nil
	})

	require.NoError(t, visitProtoPayloads(context.Background(), visitor, scheduleActivitiesRequest(n), 1))
	require.EqualValues(t, 1, peak,
		"expected at most 1 concurrent visitor call with concurrencyLimit=1")
}
