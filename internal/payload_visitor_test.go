package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/extstore"
	"google.golang.org/protobuf/proto"
)

// visitorFunc is a PayloadVisitor backed by a plain function, used in tests.
type visitorFunc func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error)

func (f visitorFunc) Visit(ctx *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return f(ctx, p)
}

// hookVisitorFunc is a PayloadVisitorWithContextHook backed by plain functions, used in tests.
type hookVisitorFunc struct {
	visit func(*proxy.VisitPayloadsContext, []*commonpb.Payload) ([]*commonpb.Payload, error)
	hook  func(context.Context, proto.Message) (context.Context, error)
}

func (v *hookVisitorFunc) Visit(ctx *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return v.visit(ctx, p)
}

func (v *hookVisitorFunc) ContextHook(ctx context.Context, msg proto.Message) (context.Context, error) {
	return v.hook(ctx, msg)
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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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

	require.NoError(t, visitProtoPayloads(t.Context(), visitor, scheduleActivitiesRequest(n), 1))
	require.EqualValues(t, 1, peak,
		"expected at most 1 concurrent visitor call with concurrencyLimit=1")
}

func TestCompositePayloadVisitor(t *testing.T) {
	t.Run("chains visitors in order", func(t *testing.T) {
		var order []int
		v1 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			order = append(order, 1)
			return p, nil
		})
		v2 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			order = append(order, 2)
			return p, nil
		})

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2}, order)
	})

	t.Run("passes transformed payloads to next visitor", func(t *testing.T) {
		extraPayload := makeTestPayload(5)
		v1 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			return append(p, extraPayload), nil
		})
		var receivedCount int
		v2 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			receivedCount = len(p)
			return p, nil
		})

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, 2, receivedCount)
	})

	t.Run("error short-circuits", func(t *testing.T) {
		expectedErr := errors.New("visitor error")
		v1 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			return nil, expectedErr
		})
		v2Called := false
		v2 := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			v2Called = true
			return p, nil
		})

		composite := newCompositePayloadVisitor(v1, v2)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.ErrorIs(t, err, expectedErr)
		require.False(t, v2Called)
	})

	t.Run("single visitor", func(t *testing.T) {
		called := false
		v := visitorFunc(func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			called = true
			return p, nil
		})
		composite := newCompositePayloadVisitor(v)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.True(t, called)
	})

	t.Run("empty visitors", func(t *testing.T) {
		composite := newCompositePayloadVisitor()
		ctx := &proxy.VisitPayloadsContext{}
		result, err := composite.Visit(ctx, []*commonpb.Payload{makeTestPayload(10)})
		require.NoError(t, err)
		require.Len(t, result, 1)
	})

	t.Run("context hook invoked on visitor implementing PayloadVisitorWithContextHook", func(t *testing.T) {
		hookCalled := false
		v := &hookVisitorFunc{
			visit: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return p, nil
			},
			hook: func(ctx context.Context, _ proto.Message) (context.Context, error) {
				hookCalled = true
				return ctx, nil
			},
		}
		composite := newCompositePayloadVisitor(v).(PayloadVisitorWithContextHook)
		_, err := composite.ContextHook(t.Context(), &commandpb.RecordMarkerCommandAttributes{})
		require.NoError(t, err)
		require.True(t, hookCalled)
	})

	t.Run("context hook chains context across visitors", func(t *testing.T) {
		type key1 struct{}
		type key2 struct{}
		v1 := &hookVisitorFunc{
			visit: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return p, nil
			},
			hook: func(ctx context.Context, _ proto.Message) (context.Context, error) {
				return context.WithValue(ctx, key1{}, true), nil
			},
		}
		var v2SawKey1 bool
		v2 := &hookVisitorFunc{
			visit: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return p, nil
			},
			hook: func(ctx context.Context, _ proto.Message) (context.Context, error) {
				v2SawKey1 = ctx.Value(key1{}) == true
				return context.WithValue(ctx, key2{}, true), nil
			},
		}
		composite := newCompositePayloadVisitor(v1, v2).(PayloadVisitorWithContextHook)
		returnedCtx, err := composite.ContextHook(t.Context(), &commandpb.RecordMarkerCommandAttributes{})
		require.NoError(t, err)
		require.True(t, v2SawKey1)
		require.Equal(t, true, returnedCtx.Value(key2{}))
	})

	t.Run("context hook error short-circuits", func(t *testing.T) {
		expectedErr := errors.New("hook error")
		v1 := &hookVisitorFunc{
			visit: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return p, nil
			},
			hook: func(ctx context.Context, _ proto.Message) (context.Context, error) {
				return nil, expectedErr
			},
		}
		v2Called := false
		v2 := &hookVisitorFunc{
			visit: func(_ *proxy.VisitPayloadsContext, p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return p, nil
			},
			hook: func(ctx context.Context, _ proto.Message) (context.Context, error) {
				v2Called = true
				return ctx, nil
			},
		}
		composite := newCompositePayloadVisitor(v1, v2).(PayloadVisitorWithContextHook)
		_, err := composite.ContextHook(t.Context(), &commandpb.RecordMarkerCommandAttributes{})
		require.ErrorIs(t, err, expectedErr)
		require.False(t, v2Called)
	})
}

// gatedStorageDriver blocks every store until released, so peak concurrency is
// observable.
type gatedStorageDriver struct {
	gate     chan struct{}
	mu       sync.Mutex
	inFlight int
	peak     int
}

func (d *gatedStorageDriver) Name() string { return "gated" }
func (d *gatedStorageDriver) Type() string { return "gated" }

func (d *gatedStorageDriver) Store(
	ctx extstore.StorageDriverStoreContext, payloads []*commonpb.Payload,
) ([]extstore.StorageDriverClaim, error) {
	claims := make([]extstore.StorageDriverClaim, len(payloads))
	var wg sync.WaitGroup
	errs := make([]error, len(payloads))
	for i := range payloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = ctx.GetLimiter().Permit(ctx.Context, payloads[i], func() error {
				d.mu.Lock()
				d.inFlight++
				if d.inFlight > d.peak {
					d.peak = d.inFlight
				}
				d.mu.Unlock()
				<-d.gate
				d.mu.Lock()
				d.inFlight--
				d.mu.Unlock()
				claims[i] = extstore.StorageDriverClaim{ClaimData: map[string]string{"k": "v"}}
				return nil
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return claims, nil
}

func (d *gatedStorageDriver) Retrieve(
	extstore.StorageDriverRetrieveContext, []extstore.StorageDriverClaim,
) ([]*commonpb.Payload, error) {
	return nil, nil
}

func (d *gatedStorageDriver) peakValue() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.peak
}

// A walk installs one MaxOperationsPerMessage limit for the whole message, on
// every path, including those that visit payload sites sequentially.
func TestVisitProtoPayloads_MessageLimitAppliesOnEveryPath(t *testing.T) {
	driver := &gatedStorageDriver{gate: make(chan struct{})}
	params, err := extstore.ExternalStorageToParams(extstore.ExternalStorage{
		Drivers:              []extstore.StorageDriver{driver},
		PayloadSizeThreshold: 1,
		Concurrency: extstore.ExternalStorageConcurrency{
			MaxDriverOperations: 100, MaxOperationsPerMessage: 2,
		},
	})
	require.NoError(t, err)

	visitor := extstore.NewExternalStorageVisitor(params)
	big := func() *commonpb.Payload {
		return &commonpb.Payload{Data: []byte(strings.Repeat("x", 64))}
	}
	// Several payload sites in one message.
	msg := &commonpb.Payloads{Payloads: []*commonpb.Payload{big(), big(), big(), big()}}

	done := make(chan error, 1)
	go func() {
		// Concurrency 4 would allow 4 at once if the limit were not shared.
		done <- visitProtoPayloads(t.Context(), visitor, msg, 4)
	}()

	require.Eventually(t, func() bool { return driver.peakValue() == 2 }, 2*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 2, driver.peakValue(), "one message must share one MaxOperationsPerMessage limit")

	close(driver.gate)
	require.NoError(t, <-done)
}
