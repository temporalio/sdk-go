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

package internal

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	historypb "go.temporal.io/api/history/v1"
	protocolpb "go.temporal.io/api/protocol/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/internal/protocol"
)

func mustSetUpdateHandler(
	t *testing.T,
	ctx Context,
	name string,
	handler interface{},
	opts UpdateHandlerOptions,
) {
	t.Helper()
	require.NoError(t, SetUpdateHandler(ctx, name, handler, opts))
}

type testUpdateScheduler struct {
	SpawnImpl func(Context, string, bool, func(Context)) Context
	YieldImpl func(Context, string)
}

type testUpdateCallbacks struct {
	AcceptImpl   func()
	RejectImpl   func(err error)
	CompleteImpl func(success interface{}, err error)
}

func (tuc *testUpdateCallbacks) Accept()          { tuc.AcceptImpl() }
func (tuc *testUpdateCallbacks) Reject(err error) { tuc.RejectImpl(err) }
func (tuc *testUpdateCallbacks) Complete(success interface{}, err error) {
	tuc.CompleteImpl(success, err)
}

func (tus *testUpdateScheduler) Spawn(ctx Context, name string, p bool, f func(Context)) Context {
	return tus.SpawnImpl(ctx, name, p, f)
}

func (tus *testUpdateScheduler) Yield(ctx Context, status string) {
	tus.YieldImpl(ctx, status)
}

var runOnCallingThread = &testUpdateScheduler{
	SpawnImpl: func(ctx Context, _ string, _ bool, f func(Context)) Context {
		f(ctx)
		return ctx
	},
	YieldImpl: func(Context, string) {},
}

var testSDKFlags = newSDKFlags(
	&workflowservice.GetSystemInfoResponse_Capabilities{SdkMetadata: true},
)

func TestUpdateHandlerPanicHandling(t *testing.T) {
	env := &workflowEnvironmentImpl{
		sdkFlags:       testSDKFlags,
		commandsHelper: newCommandsHelper(),
		dataConverter:  converter.GetDefaultDataConverter(),
		workflowInfo: &WorkflowInfo{
			Namespace:     "namespace:" + t.Name(),
			TaskQueueName: "taskqueue:" + t.Name(),
		},
	}

	t.Run("ValidateUpdate", func(t *testing.T) {
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		panicFunc := func(ctx Context) error { panic("intentional") }
		dispatcher, _ := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(t, ctx, t.Name(), panicFunc, UpdateHandlerOptions{Validator: panicFunc})
				in := UpdateInput{Name: t.Name(), Args: []interface{}{}}
				err = interceptor.inboundInterceptor.ValidateUpdate(ctx, &in)
			},
			func() bool { return false })

		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		var panicerr *PanicError
		require.ErrorAs(t, err, &panicerr,
			"panic during validate should be converted to an error to fail the update")
	})
	t.Run("ExecuteUpdate", func(t *testing.T) {
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		panicFunc := func(ctx Context) error { panic("intentional") }
		dispatcher, _ := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(t, ctx, t.Name(), panicFunc, UpdateHandlerOptions{})
				in := UpdateInput{Name: t.Name(), Args: []interface{}{}}
				err = interceptor.inboundInterceptor.ValidateUpdate(ctx, &in)
				require.Panics(t, func() {
					_, _ = interceptor.inboundInterceptor.ExecuteUpdate(ctx, &in)
				}, "panic during execution should be propagated to reach the WorkflowPanicPolicy")
			},
			func() bool { return false })
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
	})
}

func TestUpdateHandlerFnValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range [...]struct {
		check func(require.TestingT, error, ...interface{})
		fn    interface{}
	}{
		{require.Error, "not a function"},
		{require.Error, func() {}},
		{require.Error, func() int { return 0 }},
		{require.Error, func(Context, int) (int, int, error) { return 0, 0, nil }},
		{require.Error, func(int) (chan int, error) { return nil, nil }},
		{require.Error, func() error { return nil }},
		{require.Error, func(int, int, string) error { return nil }},
		{require.Error, func(int) error { return nil }},
		{require.NoError, func(Context, int) error { return nil }},
		{require.NoError, func(Context, int, int, string) error { return nil }},
		{require.NoError, func(Context) error { return nil }},
		{require.NoError, func(Context, int, int, string) error { return nil }},
	} {
		t.Run(reflect.TypeOf(tc.fn).String(), func(t *testing.T) {
			tc.check(t, validateUpdateHandlerFn(tc.fn))
		})
	}
}

func TestUpdateValidatorFnValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range [...]struct {
		check func(require.TestingT, error, ...interface{})
		fn    interface{}
	}{
		{require.Error, "not a function"},
		{require.Error, func() {}},
		{require.Error, func(int) (int, error) { return 0, nil }},
		{require.Error, func(int, int, string) (int, error) { return 0, nil }},
		{require.Error, func(int) (chan int, error) { return nil, nil }},
		{require.NoError, func(int, int, string) error { return nil }},
		{require.NoError, func() error { return nil }},
		{require.NoError, func(int) error { return nil }},
	} {
		t.Run(reflect.TypeOf(tc.fn).String(), func(t *testing.T) {
			tc.check(t, validateValidatorFn(tc.fn))
		})
	}
}

func TestDefaultUpdateHandler(t *testing.T) {
	t.Parallel()

	dc := converter.GetDefaultDataConverter()
	createTestWfEnv := func() *workflowEnvironmentImpl {
		return &workflowEnvironmentImpl{
			sdkFlags:       testSDKFlags,
			commandsHelper: newCommandsHelper(),
			dataConverter:  dc,
			workflowInfo: &WorkflowInfo{
				Namespace:     "namespace:" + t.Name(),
				TaskQueueName: "taskqueue:" + t.Name(),
			},
			bufferedUpdateRequests: make(map[string][]func()),
		}
	}

	hdr := &commonpb.Header{Fields: map[string]*commonpb.Payload{}}
	argStr := t.Name()
	args, err := dc.ToPayloads(argStr)
	require.NoError(t, err)

	t.Run("no handler registered", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(
					t,
					ctx,
					"unused_handler",
					func(ctx Context) error { panic("should not be called") },
					UpdateHandlerOptions{},
				)
			},
			env.DrainUnhandledUpdates)
		var rejectErr error
		defaultUpdateHandler(ctx, "will_not_be_found", "testID", args, hdr, &testUpdateCallbacks{
			RejectImpl: func(err error) { rejectErr = err },
		}, runOnCallingThread)
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		require.ErrorContains(t, rejectErr, "unknown update")
		require.ErrorContains(t, rejectErr, "unused_handler",
			"handler not found error should include a list of the registered handlers")
	})

	t.Run("malformed serialized input", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(
					t,
					ctx,
					t.Name(),
					func(Context, int) error { return nil },
					UpdateHandlerOptions{},
				)
			},
			env.DrainUnhandledUpdates)

		junkArgs := &commonpb.Payloads{Payloads: []*commonpb.Payload{&commonpb.Payload{}}}
		var rejectErr error
		defaultUpdateHandler(ctx, t.Name(), "testID", junkArgs, hdr, &testUpdateCallbacks{
			RejectImpl: func(err error) { rejectErr = err },
		}, runOnCallingThread)
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		require.ErrorContains(t, rejectErr, "unable to decode")
	})

	t.Run("reject from validator", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		updateFunc := func(Context, string) error { panic("should not get called") }
		validatorFunc := func(Context, string) error { return errors.New("expected") }
		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(
					t,
					ctx,
					t.Name(),
					updateFunc,
					UpdateHandlerOptions{Validator: validatorFunc},
				)
			},
			env.DrainUnhandledUpdates)
		var rejectErr error
		defaultUpdateHandler(ctx, t.Name(), "testID", args, hdr, &testUpdateCallbacks{
			RejectImpl: func(err error) { rejectErr = err },
		}, runOnCallingThread)
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		require.Equal(t, validatorFunc(ctx, argStr), rejectErr)
	})

	t.Run("illegal state panic from validator", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		updateFunc := func(Context, string) error { panic("should not get called") }
		validatorFunc := func(Context, string) error { panic(panicIllegalAccessCoroutineState) }
		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(
					t,
					ctx,
					t.Name(),
					updateFunc,
					UpdateHandlerOptions{Validator: validatorFunc},
				)
			},
			env.DrainUnhandledUpdates)
		defaultUpdateHandler(ctx, t.Name(), "testID", args, hdr, &testUpdateCallbacks{}, runOnCallingThread)
		require.Error(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
	})

	t.Run("error from update func", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		updateFunc := func(Context, string) error { return errors.New("expected") }
		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(t, ctx, t.Name(), updateFunc, UpdateHandlerOptions{})
			},
			env.DrainUnhandledUpdates)
		var (
			resultErr error
			accepted  bool
			result    interface{}
		)
		defaultUpdateHandler(ctx, t.Name(), "testID", args, hdr, &testUpdateCallbacks{
			AcceptImpl: func() { accepted = true },
			CompleteImpl: func(success interface{}, err error) {
				resultErr = err
				result = success
			},
		}, runOnCallingThread)

		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		require.True(t, accepted)
		require.Equal(t, updateFunc(ctx, argStr), resultErr)
		require.Nil(t, result)
	})

	t.Run("update success", func(t *testing.T) {
		env := createTestWfEnv()
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)

		updateFunc := func(ctx Context, s string) (string, error) { return s + " success!", nil }
		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(t, ctx, t.Name(), updateFunc, UpdateHandlerOptions{})
			},
			env.DrainUnhandledUpdates)
		var (
			resultErr error
			accepted  bool
			result    interface{}
		)
		defaultUpdateHandler(ctx, t.Name(), "testID", args, hdr, &testUpdateCallbacks{
			AcceptImpl: func() { accepted = true },
			CompleteImpl: func(success interface{}, err error) {
				resultErr = err
				result = success
			},
		}, runOnCallingThread)
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(10*time.Second))
		require.True(t, accepted)
		require.Nil(t, resultErr)

		expectedResult, _ := updateFunc(ctx, argStr)
		require.Equal(t, expectedResult, result)
	})

	t.Run("update before handlers registered", func(t *testing.T) {
		env := createTestWfEnv()
		// same test as above except that we don't set the update handler for
		// t.Name() until the first Yield. This emulates the situation where
		// there is an update in the first WFT of a workflow so the SDK needs to
		// wait for the workflow function to execute up to the point where it
		// has registered some update handlers. If the SDK attempts to deliver
		// the update before the first run of the workflow function, no handlers
		// will be registered yet.

		// don't reuse the context that has all the other update handlers
		// registered because the code under test will think the handler
		// registration at workflow start time has already occurred
		interceptor, ctx, err := newWorkflowContext(env, nil)
		require.NoError(t, err)
		updateFunc := func(ctx Context, s string) (string, error) { return s + " success!", nil }
		dispatcher, ctx := newDispatcher(
			ctx,
			interceptor,
			func(ctx Context) {
				mustSetUpdateHandler(t, ctx, t.Name(), updateFunc, UpdateHandlerOptions{})
			},
			func() bool { return false })

		var (
			resultErr error
			rejectErr error
			accepted  bool
			result    interface{}
		)
		sched := &testUpdateScheduler{
			SpawnImpl: func(ctx Context, _ string, _ bool, f func(Context)) Context {
				f(ctx)
				return ctx
			},
			YieldImpl: func(ctx Context, _ string) {
				// set the handler in place here
				mustSetUpdateHandler(t, ctx, t.Name(), updateFunc, UpdateHandlerOptions{})
			},
		}
		defaultUpdateHandler(ctx, t.Name(), "testID", args, hdr, &testUpdateCallbacks{
			RejectImpl: func(err error) { rejectErr = err },
			AcceptImpl: func() { accepted = true },
			CompleteImpl: func(success interface{}, err error) {
				resultErr = err
				result = success
			},
		}, sched)
		require.NoError(t, dispatcher.ExecuteUntilAllBlocked(time.Second))

		require.True(t, accepted)
		require.Nil(t, resultErr)
		require.Nil(t, rejectErr)

		expectedResult, _ := updateFunc(ctx, argStr)
		require.Equal(t, expectedResult, result)
	})

}

func TestInvalidUpdateStateTransitions(t *testing.T) {
	// these would all reflect programming errors so we expect panics
	stubUpdateHandler := func(string, string, *commonpb.Payloads, *commonpb.Header, UpdateCallbacks) {}
	requestMsg := protocolpb.Message{
		Id:                 t.Name() + "-id",
		ProtocolInstanceId: t.Name() + "-proto-id",
		Body:               protocol.MustMarshalAny(&updatepb.Request{}),
	}
	env := &workflowEnvironmentImpl{
		sdkFlags:       testSDKFlags,
		commandsHelper: newCommandsHelper(),
		dataConverter:  converter.GetDefaultDataConverter(),
	}
	t.Run("cannot complete from new state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.Panics(t, func() { up.Complete("the result", nil) })
	})
	t.Run("cannot accept from new state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.Panics(t, func() { up.Accept() })
	})
	t.Run("cannot complete from requested state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		require.Panics(t, func() { up.Complete("the result", nil) })
	})
	t.Run("cannot request from requested state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		require.Panics(t, func() { _ = up.HandleMessage(&requestMsg) })
	})
	t.Run("cannot request from accepted state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		up.Accept()
		require.Panics(t, func() { _ = up.HandleMessage(&requestMsg) })
	})
	t.Run("cannot reject from accepted state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		up.Accept()
		require.Panics(t, func() { up.Reject(errors.New("reject")) })
	})
	t.Run("cannot request from completed state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		up.Accept()
		up.Complete("success", nil)
		require.True(t, up.HasCompleted())
		require.Panics(t, func() { _ = up.HandleMessage(&requestMsg) })
	})
	t.Run("cannot accept from completed state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		up.Accept()
		up.Complete("success", nil)
		require.True(t, up.HasCompleted())
		require.Panics(t, func() { up.Accept() })
	})
	t.Run("cannot reject from completed state", func(t *testing.T) {
		up := newUpdateProtocol(t.Name(), stubUpdateHandler, env)
		require.NoError(t, up.HandleMessage(&requestMsg))
		up.Accept()
		up.Complete("success", nil)
		require.True(t, up.HasCompleted())
		require.Panics(t, func() { up.Reject(errors.New("reject")) })
	})
}

func TestCompletedEventPredicate(t *testing.T) {
	updateID := t.Name() + "-update-id"
	stubUpdateHandler := func(string, string, *commonpb.Payloads, *commonpb.Header, UpdateCallbacks) {}
	requestMsg := protocolpb.Message{
		Id:                 t.Name() + "-id",
		ProtocolInstanceId: updateID,
		Body:               protocol.MustMarshalAny(&updatepb.Request{}),
	}
	env := &workflowEnvironmentImpl{
		sdkFlags:       testSDKFlags,
		commandsHelper: newCommandsHelper(),
		dataConverter:  converter.GetDefaultDataConverter(),
	}
	up := newUpdateProtocol(updateID, stubUpdateHandler, env)
	require.NoError(t, up.HandleMessage(&requestMsg))
	up.Accept()
	up.Complete("success", nil)
	require.Len(t, env.outbox, 2, "expected to find accepted and completed messages")

	pred := env.outbox[1].eventPredicate

	require.False(t, pred(&historypb.HistoryEvent{}))
	require.False(t, pred(&historypb.HistoryEvent{
		Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateCompletedEventAttributes{
			WorkflowExecutionUpdateCompletedEventAttributes: &historypb.WorkflowExecutionUpdateCompletedEventAttributes{
				Meta: &updatepb.Meta{UpdateId: "some other update ID"},
			},
		},
	}))
	require.True(t, pred(&historypb.HistoryEvent{
		Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateCompletedEventAttributes{
			WorkflowExecutionUpdateCompletedEventAttributes: &historypb.WorkflowExecutionUpdateCompletedEventAttributes{
				Meta: &updatepb.Meta{UpdateId: updateID},
			},
		},
	}))
}

func TestAcceptedEventPredicate(t *testing.T) {
	updateID := t.Name() + "-update-id"
	requestMsgID := t.Name() + "request-msg-id"
	requestSeqID := int64(1234)
	stubUpdateHandler := func(string, string, *commonpb.Payloads, *commonpb.Header, UpdateCallbacks) {}
	request := updatepb.Request{
		Meta: &updatepb.Meta{UpdateId: updateID},
	}
	requestMsg := protocolpb.Message{
		Id:                 requestMsgID,
		SequencingId:       &protocolpb.Message_EventId{EventId: requestSeqID},
		ProtocolInstanceId: updateID,
		Body:               protocol.MustMarshalAny(&request),
	}
	env := &workflowEnvironmentImpl{
		sdkFlags:       testSDKFlags,
		commandsHelper: newCommandsHelper(),
		dataConverter:  converter.GetDefaultDataConverter(),
	}
	up := newUpdateProtocol(updateID, stubUpdateHandler, env)
	require.NoError(t, up.HandleMessage(&requestMsg))
	up.Accept()
	require.Len(t, env.outbox, 1, "expected to find accepted message")

	var acptmsg updatepb.Acceptance
	require.NoError(t, env.outbox[0].msg.Body.UnmarshalTo(&acptmsg))
	require.Nil(t, acptmsg.AcceptedRequest,
		"do not send the original request back - this field will be removed soon")

	pred := env.outbox[0].eventPredicate
	for _, tc := range [...]struct {
		name  string
		attrs *historypb.WorkflowExecutionUpdateAcceptedEventAttributes
		test  require.BoolAssertionFunc
	}{
		{
			name: "wrong req msg ID",
			test: require.False,
			attrs: &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
				ProtocolInstanceId:               updateID,
				AcceptedRequest:                  &request,
				AcceptedRequestMessageId:         "wrong request message ID",
				AcceptedRequestSequencingEventId: requestSeqID,
			},
		},
		{
			name: "wrong req seq ID",
			test: require.False,
			attrs: &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
				ProtocolInstanceId:               updateID,
				AcceptedRequest:                  &request,
				AcceptedRequestMessageId:         requestMsgID,
				AcceptedRequestSequencingEventId: requestSeqID + 10,
			},
		},
		{
			name: "match",
			test: require.True,
			attrs: &historypb.WorkflowExecutionUpdateAcceptedEventAttributes{
				AcceptedRequest:                  &request,
				ProtocolInstanceId:               updateID,
				AcceptedRequestMessageId:         requestMsgID,
				AcceptedRequestSequencingEventId: requestSeqID,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := historypb.HistoryEvent{
				Attributes: &historypb.HistoryEvent_WorkflowExecutionUpdateAcceptedEventAttributes{
					WorkflowExecutionUpdateAcceptedEventAttributes: tc.attrs,
				},
			}
			tc.test(t, pred(&event))
		})
	}
}
