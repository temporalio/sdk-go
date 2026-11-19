package internal

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestWithOptionsAPIsUseDistinctOutboundInterceptorMethods(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var trace []string
	env.SetWorkerOptions(WorkerOptions{Interceptors: []WorkerInterceptor{
		&workerInterceptor{newOutbound: func(next WorkflowOutboundInterceptor) WorkflowOutboundInterceptor {
			return &plainOnlyOutboundInterceptor{WorkflowOutboundInterceptorBase{Next: next}, &trace}
		}},
		&workerInterceptor{newOutbound: func(next WorkflowOutboundInterceptor) WorkflowOutboundInterceptor {
			return &withOptionsOnlyOutboundInterceptor{WorkflowOutboundInterceptorBase{Next: next}, &trace}
		}},
	}})
	env.OnSignalExternalWorkflow(mock.Anything, "target", "", "sig", "arg").Return(nil)
	env.OnRequestCancelExternalWorkflow(defaultTestNamespace, "target", "").Return(nil)

	childWorkflowFn := func(ctx Context) error {
		ch := GetSignalChannel(ctx, "child-sig")
		ch.Receive(ctx, nil)
		ch.Receive(ctx, nil)
		return nil
	}
	env.RegisterWorkflow(childWorkflowFn)

	key := NewSearchAttributeKeyKeyword("CustomKeywordField")
	retryPolicy := &RetryPolicy{MaximumAttempts: 3}
	var continueAsNewWithOptionsErr, continueAsNewErr error
	env.ExecuteWorkflow(func(ctx Context) error {
		groups := []EventGroup{NewEventGroup("g")}
		child := ExecuteChildWorkflow(WithChildWorkflowOptions(ctx, ChildWorkflowOptions{}), childWorkflowFn)
		if err := child.SignalChildWorkflowWithOptions(ctx, "child-sig", "arg", SignalChildWorkflowOptions{EventGroups: groups}).Get(ctx, nil); err != nil {
			return err
		}
		if err := child.SignalChildWorkflow(ctx, "child-sig", "arg").Get(ctx, nil); err != nil {
			return err
		}
		if err := child.Get(ctx, nil); err != nil {
			return err
		}
		GetVersionWithOptions(ctx, "change-1", DefaultVersion, 1, GetVersionOptions{EventGroups: groups})
		GetVersion(ctx, "change-2", DefaultVersion, 1)
		if err := SignalExternalWorkflowWithOptions(ctx, "target", "", "sig", "arg", SignalExternalWorkflowOptions{EventGroups: groups}).Get(ctx, nil); err != nil {
			return err
		}
		if err := SignalExternalWorkflow(ctx, "target", "", "sig", "arg").Get(ctx, nil); err != nil {
			return err
		}
		if err := RequestCancelExternalWorkflowWithOptions(ctx, "target", "", CancelExternalWorkflowOptions{EventGroups: groups}).Get(ctx, nil); err != nil {
			return err
		}
		if err := RequestCancelExternalWorkflow(ctx, "target", "").Get(ctx, nil); err != nil {
			return err
		}
		if err := UpsertTypedSearchAttributesWithOptions(ctx, UpsertSearchAttributesOptions{EventGroups: groups}, key.ValueSet("a")); err != nil {
			return err
		}
		if err := UpsertTypedSearchAttributes(ctx, key.ValueSet("b")); err != nil {
			return err
		}
		if err := UpsertMemoWithOptions(ctx, map[string]any{"k": 1}, UpsertMemoOptions{EventGroups: groups}); err != nil {
			return err
		}
		if err := UpsertMemo(ctx, map[string]any{"k": 2}); err != nil {
			return err
		}
		// Constructing the errors is what goes through the interceptors; they are
		// not returned so the workflow completes instead of continuing as new.
		continueAsNewWithOptionsErr = NewContinueAsNewErrorWithOptions(ctx, ContinueAsNewErrorOptions{
			RetryPolicy:               retryPolicy,
			BackoffStartInterval:      time.Minute,
			InitialVersioningBehavior: ContinueAsNewVersioningBehaviorAutoUpgrade,
			EventGroups:               groups,
		}, childWorkflowFn)
		continueAsNewErr = NewContinueAsNewError(ctx, childWorkflowFn)
		return nil
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, []string{
		"SignalChildWorkflowWithOptions [g]",
		"SignalChildWorkflow",
		"GetVersionWithOptions [g]",
		"GetVersion",
		"SignalExternalWorkflowWithOptions [g]",
		"SignalExternalWorkflow",
		"RequestCancelExternalWorkflowWithOptions [g]",
		"RequestCancelExternalWorkflow",
		"UpsertTypedSearchAttributesWithOptions [g]",
		"UpsertTypedSearchAttributes",
		"UpsertMemoWithOptions [g]",
		"UpsertMemo",
		"NewContinueAsNewErrorWithOptions [g]",
		"NewContinueAsNewError",
	}, trace)

	var withOptions *ContinueAsNewError
	require.ErrorAs(t, continueAsNewWithOptionsErr, &withOptions)
	require.Same(t, retryPolicy, withOptions.RetryPolicy)
	require.Equal(t, time.Minute, withOptions.BackoffStartInterval)
	require.Equal(t, ContinueAsNewVersioningBehavior(ContinueAsNewVersioningBehaviorAutoUpgrade), withOptions.InitialVersioningBehavior)
	require.Len(t, withOptions.eventGroupMarkers, 1)
	require.Equal(t, "g", withOptions.eventGroupMarkers[0].GetLabel().GetId())

	var plain *ContinueAsNewError
	require.ErrorAs(t, continueAsNewErr, &plain)
	require.Nil(t, plain.RetryPolicy)
	require.Zero(t, plain.BackoffStartInterval)
	require.Zero(t, plain.InitialVersioningBehavior)
	require.Empty(t, plain.eventGroupMarkers)
}

type workerInterceptor struct {
	WorkerInterceptorBase
	newOutbound func(next WorkflowOutboundInterceptor) WorkflowOutboundInterceptor
}

func (w *workerInterceptor) InterceptWorkflow(ctx Context, next WorkflowInboundInterceptor) WorkflowInboundInterceptor {
	return &workflowInboundInterceptor{WorkflowInboundInterceptorBase{Next: next}, w.newOutbound}
}

type workflowInboundInterceptor struct {
	WorkflowInboundInterceptorBase
	newOutbound func(next WorkflowOutboundInterceptor) WorkflowOutboundInterceptor
}

func (w *workflowInboundInterceptor) Init(outbound WorkflowOutboundInterceptor) error {
	return w.Next.Init(w.newOutbound(outbound))
}

type plainOnlyOutboundInterceptor struct {
	WorkflowOutboundInterceptorBase
	trace *[]string
}

func (w *plainOnlyOutboundInterceptor) GetVersion(ctx Context, changeID string, minSupported, maxSupported Version) Version {
	*w.trace = append(*w.trace, "GetVersion")
	return w.Next.GetVersion(ctx, changeID, minSupported, maxSupported)
}

func (w *plainOnlyOutboundInterceptor) SignalExternalWorkflow(ctx Context, workflowID, runID, signalName string, arg any) Future {
	*w.trace = append(*w.trace, "SignalExternalWorkflow")
	return w.Next.SignalExternalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (w *plainOnlyOutboundInterceptor) SignalChildWorkflow(ctx Context, workflowID, signalName string, arg any) Future {
	*w.trace = append(*w.trace, "SignalChildWorkflow")
	return w.Next.SignalChildWorkflow(ctx, workflowID, signalName, arg)
}

func (w *plainOnlyOutboundInterceptor) RequestCancelExternalWorkflow(ctx Context, workflowID, runID string) Future {
	*w.trace = append(*w.trace, "RequestCancelExternalWorkflow")
	return w.Next.RequestCancelExternalWorkflow(ctx, workflowID, runID)
}

func (w *plainOnlyOutboundInterceptor) UpsertTypedSearchAttributes(ctx Context, attributes ...SearchAttributeUpdate) error {
	*w.trace = append(*w.trace, "UpsertTypedSearchAttributes")
	return w.Next.UpsertTypedSearchAttributes(ctx, attributes...)
}

func (w *plainOnlyOutboundInterceptor) UpsertMemo(ctx Context, memo map[string]any) error {
	*w.trace = append(*w.trace, "UpsertMemo")
	return w.Next.UpsertMemo(ctx, memo)
}

func (w *plainOnlyOutboundInterceptor) NewContinueAsNewError(ctx Context, wfn any, args ...any) error {
	*w.trace = append(*w.trace, "NewContinueAsNewError")
	return w.Next.NewContinueAsNewError(ctx, wfn, args...)
}

type withOptionsOnlyOutboundInterceptor struct {
	WorkflowOutboundInterceptorBase
	trace *[]string
}

func (w *withOptionsOnlyOutboundInterceptor) record(method string, groups []EventGroup) {
	*w.trace = append(*w.trace, fmt.Sprintf("%s %v", method, eventGroupIDs(groups)))
}

func (w *withOptionsOnlyOutboundInterceptor) GetVersionWithOptions(ctx Context, changeID string, minSupported, maxSupported Version, options GetVersionOptions) Version {
	w.record("GetVersionWithOptions", options.EventGroups)
	return w.Next.GetVersionWithOptions(ctx, changeID, minSupported, maxSupported, options)
}

func (w *withOptionsOnlyOutboundInterceptor) SignalExternalWorkflowWithOptions(ctx Context, workflowID, runID, signalName string, arg any, options SignalExternalWorkflowOptions) Future {
	w.record("SignalExternalWorkflowWithOptions", options.EventGroups)
	return w.Next.SignalExternalWorkflowWithOptions(ctx, workflowID, runID, signalName, arg, options)
}

func (w *withOptionsOnlyOutboundInterceptor) SignalChildWorkflowWithOptions(ctx Context, workflowID, signalName string, arg any, options SignalChildWorkflowOptions) Future {
	w.record("SignalChildWorkflowWithOptions", options.EventGroups)
	return w.Next.SignalChildWorkflowWithOptions(ctx, workflowID, signalName, arg, options)
}

func (w *withOptionsOnlyOutboundInterceptor) RequestCancelExternalWorkflowWithOptions(ctx Context, workflowID, runID string, options CancelExternalWorkflowOptions) Future {
	w.record("RequestCancelExternalWorkflowWithOptions", options.EventGroups)
	return w.Next.RequestCancelExternalWorkflowWithOptions(ctx, workflowID, runID, options)
}

func (w *withOptionsOnlyOutboundInterceptor) UpsertTypedSearchAttributesWithOptions(ctx Context, attributes []SearchAttributeUpdate, options UpsertSearchAttributesOptions) error {
	w.record("UpsertTypedSearchAttributesWithOptions", options.EventGroups)
	return w.Next.UpsertTypedSearchAttributesWithOptions(ctx, attributes, options)
}

func (w *withOptionsOnlyOutboundInterceptor) UpsertMemoWithOptions(ctx Context, memo map[string]any, options UpsertMemoOptions) error {
	w.record("UpsertMemoWithOptions", options.EventGroups)
	return w.Next.UpsertMemoWithOptions(ctx, memo, options)
}

func (w *withOptionsOnlyOutboundInterceptor) NewContinueAsNewErrorWithOptions(ctx Context, options ContinueAsNewErrorOptions, wfn any, args ...any) error {
	w.record("NewContinueAsNewErrorWithOptions", options.EventGroups)
	return w.Next.NewContinueAsNewErrorWithOptions(ctx, options, wfn, args...)
}
