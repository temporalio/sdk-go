package internal

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonpb "go.temporal.io/api/common/v1"
	sandboxpb "go.temporal.io/api/sandbox/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/internal/common/metrics"
	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

// mockSandbox records Exec calls and returns canned results.
type mockSandbox struct {
	mu       sync.Mutex
	execCmds []string
	result   *ExecResult
	execErr  error
	closed   bool
}

func (s *mockSandbox) Exec(_ context.Context, cmd string, _ ExecOptions) (*ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.execCmds = append(s.execCmds, cmd)
	if s.execErr != nil {
		return nil, s.execErr
	}
	if s.result != nil {
		return s.result, nil
	}
	return &ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
}

func (s *mockSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// mockSandboxProvider creates mockSandbox instances and records calls.
type mockSandboxProvider struct {
	mu        sync.Mutex
	sandbox   *mockSandbox
	opts      SandboxOptions
	createErr error
}

func (p *mockSandboxProvider) CreateSandbox(_ context.Context, opts SandboxOptions) (Sandbox, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opts = opts
	if p.createErr != nil {
		return nil, p.createErr
	}
	sb := &mockSandbox{result: &ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}}
	p.sandbox = sb
	return sb, nil
}

// failingSandboxProvider always fails CreateSandbox.
type failingSandboxProvider struct{}

func (p *failingSandboxProvider) CreateSandbox(_ context.Context, _ SandboxOptions) (Sandbox, error) {
	return nil, fmt.Errorf("runsc not found")
}

// --- helpers ---

func newTestActivityHandler(t *testing.T, logger log.Logger, reg *registry, provider SandboxProvider) (ActivityTaskHandler, *workflowservicemock.MockWorkflowServiceClient) {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	mockService := workflowservicemock.NewMockWorkflowServiceClient(mockCtrl)
	client := WorkflowClient{workflowService: mockService}

	cache := NewWorkerCache()
	wep := workerExecutionParameters{
		TaskQueue:        "test-sandbox-tq",
		Namespace:        "test-ns",
		Identity:         "test-id",
		MetricsHandler:   metrics.NopHandler,
		Logger:           logger,
		FailureConverter: GetDefaultFailureConverter(),
		cache:            cache,
		sandboxProvider:  provider,
		capabilities: &workflowservice.GetSystemInfoResponse_Capabilities{
			SignalAndQueryHeader:            true,
			InternalErrorDifferentiation:    true,
			ActivityFailureIncludeHeartbeat: true,
			EncodedFailureAttributes:        true,
		},
	}
	return newActivityTaskHandler(&client, wep, reg), mockService
}

func newPollResponse(activityName string, sbOpts *sandboxpb.SandboxOptions) *workflowservice.PollActivityTaskQueueResponse {
	now := time.Now()
	return &workflowservice.PollActivityTaskQueueResponse{
		Attempt:   1,
		TaskToken: []byte("token"),
		WorkflowExecution: &commonpb.WorkflowExecution{
			WorkflowId: "wID",
			RunId:      "rID",
		},
		ActivityType:           &commonpb.ActivityType{Name: activityName},
		ActivityId:             uuid.NewString(),
		ScheduledTime:          timestamppb.New(now),
		ScheduleToCloseTimeout: durationpb.New(10 * time.Second),
		StartedTime:            timestamppb.New(now),
		StartToCloseTimeout:    durationpb.New(10 * time.Second),
		WorkflowType:           &commonpb.WorkflowType{Name: "wType"},
		WorkflowNamespace:      "test-ns",
		SandboxOptions:         sbOpts,
	}
}

func simpleSandboxOptions() *sandboxpb.SandboxOptions {
	return &sandboxpb.SandboxOptions{
		ResourceLimits: &sandboxpb.SandboxResourceLimits{
			Cpus:     1.0,
			MemoryMb: 64,
			MaxPids:  50,
		},
	}
}

// --- tests ---

// TestSandboxHandler_ActivityWithSandbox verifies that when an activity has
// SandboxOptions set, the sandbox is created and accessible, and closed after
// the activity completes.
func TestSandboxHandler_ActivityWithSandbox(t *testing.T) {
	logger := ilog.NewDefaultLogger()
	provider := &mockSandboxProvider{}

	reg := newRegistry()
	activityName := "sandboxActivity"
	reg.RegisterActivityWithOptions(func(ctx context.Context) (string, error) {
		sb, err := GetSandbox(ctx)
		if err != nil {
			return "", err
		}
		result, err := sb.Exec(ctx, "echo hello", ExecOptions{})
		if err != nil {
			return "", err
		}
		return string(result.Stdout), nil
	}, RegisterActivityOptions{Name: activityName})

	handler, _ := newTestActivityHandler(t, logger, reg, provider)
	pats := newPollResponse(activityName, simpleSandboxOptions())

	result, err := handler.Execute("test-tq", pats)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Sandbox was created and used.
	require.NotNil(t, provider.sandbox)
	require.Equal(t, []string{"echo hello"}, provider.sandbox.execCmds)
	// Sandbox was closed after activity completed.
	require.True(t, provider.sandbox.closed)
}

// TestSandboxHandler_ActivityWithoutSandboxOpts verifies that activities
// without SandboxOptions work normally and no sandbox is created.
func TestSandboxHandler_ActivityWithoutSandboxOpts(t *testing.T) {
	logger := ilog.NewDefaultLogger()
	provider := &mockSandboxProvider{}

	reg := newRegistry()
	activityName := "noSandboxActivity"
	reg.RegisterActivityWithOptions(func(ctx context.Context) (string, error) {
		// Calling GetSandbox should fail — no SandboxOptions set.
		_, err := GetSandbox(ctx)
		if err == nil {
			return "", fmt.Errorf("expected GetSandbox to fail without SandboxOptions")
		}
		return "no sandbox needed", nil
	}, RegisterActivityOptions{Name: activityName})

	handler, _ := newTestActivityHandler(t, logger, reg, provider)
	// No SandboxOptions on the poll response.
	pats := newPollResponse(activityName, nil)

	result, err := handler.Execute("test-tq", pats)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Provider was never called.
	require.Nil(t, provider.sandbox)
}

// TestSandboxHandler_MissingSandboxProvider verifies that when SandboxOptions
// is set but no SandboxProvider is configured on the worker, GetSandbox
// returns a clear error.
func TestSandboxHandler_MissingSandboxProvider(t *testing.T) {
	logger := ilog.NewDefaultLogger()

	reg := newRegistry()
	activityName := "missingSandboxProvider"
	reg.RegisterActivityWithOptions(func(ctx context.Context) error {
		_, err := GetSandbox(ctx)
		if err == nil {
			return fmt.Errorf("expected error for missing provider")
		}
		// Verify the error message is helpful.
		require.Contains(t, err.Error(), "no SandboxProvider configured")
		return nil
	}, RegisterActivityOptions{Name: activityName})

	// No sandbox provider configured.
	handler, _ := newTestActivityHandler(t, logger, reg, nil)
	pats := newPollResponse(activityName, simpleSandboxOptions())

	result, err := handler.Execute("test-tq", pats)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestSandboxHandler_ActivityFailure verifies that when the activity returns
// an error, the sandbox is still closed.
func TestSandboxHandler_ActivityFailure(t *testing.T) {
	logger := ilog.NewDefaultLogger()
	provider := &mockSandboxProvider{}

	reg := newRegistry()
	activityName := "failingActivity"
	reg.RegisterActivityWithOptions(func(ctx context.Context) error {
		sb, err := GetSandbox(ctx)
		if err != nil {
			return err
		}
		_, _ = sb.Exec(ctx, "do-work", ExecOptions{})
		return fmt.Errorf("activity failed on purpose")
	}, RegisterActivityOptions{Name: activityName})

	handler, _ := newTestActivityHandler(t, logger, reg, provider)
	pats := newPollResponse(activityName, simpleSandboxOptions())

	result, err := handler.Execute("test-tq", pats)
	// Activity errors are returned as a response (not a Go error).
	require.NoError(t, err)
	require.NotNil(t, result)

	// Sandbox was created and closed despite activity failure.
	require.NotNil(t, provider.sandbox)
	require.True(t, provider.sandbox.closed)
}

// TestSandboxHandler_SandboxNeverAccessed verifies that when an activity has
// SandboxOptions but never calls GetSandbox, no sandbox is created (lazy)
// and Close is a no-op.
func TestSandboxHandler_SandboxNeverAccessed(t *testing.T) {
	logger := ilog.NewDefaultLogger()
	provider := &mockSandboxProvider{}

	reg := newRegistry()
	activityName := "noAccessActivity"
	reg.RegisterActivityWithOptions(func(ctx context.Context) (string, error) {
		// Activity has SandboxOptions but doesn't use the sandbox.
		return "done without sandbox", nil
	}, RegisterActivityOptions{Name: activityName})

	handler, _ := newTestActivityHandler(t, logger, reg, provider)
	pats := newPollResponse(activityName, simpleSandboxOptions())

	result, err := handler.Execute("test-tq", pats)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Sandbox was never created (lazy).
	require.Nil(t, provider.sandbox)
}

// TestSandboxHandler_CreateSandboxFails verifies that when sandbox creation
// fails, the error is surfaced to the activity via GetSandbox.
func TestSandboxHandler_CreateSandboxFails(t *testing.T) {
	logger := ilog.NewDefaultLogger()
	provider := &mockSandboxProvider{createErr: fmt.Errorf("runsc not found")}

	reg := newRegistry()
	activityName := "failCreate"
	reg.RegisterActivityWithOptions(func(ctx context.Context) error {
		_, err := GetSandbox(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "runsc not found")
		return err
	}, RegisterActivityOptions{Name: activityName})

	handler, _ := newTestActivityHandler(t, logger, reg, provider)
	pats := newPollResponse(activityName, simpleSandboxOptions())

	result, err := handler.Execute("test-tq", pats)
	// Activity errors are returned as a response (not a Go error).
	require.NoError(t, err)
	require.NotNil(t, result)
}
