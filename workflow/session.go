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

package workflow

import (
	"go.temporal.io/sdk/internal"
)

type (
	// SessionInfo contains information of a created session. For now, exported
	// fields are SessionID and HostName.
	// SessionID is a uuid generated when CreateSession() or RecreateSession()
	// is called and can be used to uniquely identify a session.
	// HostName specifies which host is executing the session
	SessionInfo = internal.SessionInfo

	// SessionOptions specifies metadata for a session.
	// ExecutionTimeout: required, no default
	//     Specifies the maximum amount of time the session can run
	// CreationTimeout: required, no default
	//     Specifies how long session creation can take before returning an error
	// HeartbeatTimeout: optional, default 20s
	//     Specifies the heartbeat timeout. If heartbeat is not received by server
	//     within the timeout, the session will be declared as failed
	SessionOptions = internal.SessionOptions
)

// ErrSessionFailed is the error returned when user tries to execute an activity but the
// session it belongs to has already failed
var ErrSessionFailed = internal.ErrSessionFailed

// Note: Worker should be configured to process session. To do this, set the following
// fields in WorkerOptions:
//     EnableSessionWorker: true
//     MaxConcurrentSessionExecutionSize: the maximum number of concurrently sessions the resource
//         support. By default, 1000 is used.

// CreateSession creates a session and returns a new context which contains information
// of the created session. The session will be created on the taskqueue user specified in
// ActivityOptions. If none is specified, the default one will be used.
//
// CreationSession will fail in the following situations:
//     1. The context passed in already contains a session which is still open
//        (not closed and failed).
//     2. All the workers are busy (number of sessions currently running on all the workers have reached
//        MaxConcurrentSessionExecutionSize, which is specified when starting the workers) and session
//        cannot be created within a specified timeout.
//
// If an activity is executed using the returned context, it's regarded as part of the
// session. All activities within the same session will be executed by the same worker.
// User still needs to handle the error returned when executing an activity. Session will
// not be marked as failed if an activity within it returns an error. Only when the worker
// executing the session is down, that session will be marked as failed. Executing an activity
// within a failed session will return ErrSessionFailed immediately without scheduling that activity.
//
// The returned session Context will be canceled if the session fails (worker died) or CompleteSession()
// is called. This means that in these two cases, all user activities scheduled using the returned session
// Context will also be canceled.
//
// If user wants to end a session since activity returns some error, use CompleteSession API below.
// New session can be created if necessary to retry the whole session.
//
// Example:
//    so := &SessionOptions{
// 	      ExecutionTimeout: time.Minute,
// 	      CreationTimeout:  time.Minute,
//    }
//    sessionCtx, err := CreateSession(ctx, so)
//    if err != nil {
//		    // Creation failed. Wrong ctx or too many outstanding sessions.
//    }
//    defer CompleteSession(sessionCtx)
//    err = ExecuteActivity(sessionCtx, someActivityFunc, activityInput).Get(sessionCtx, nil)
//    if err == ErrSessionFailed {
//        // Session has failed
//    } else {
//        // Handle activity error
//    }
//    ... // execute more activities using sessionCtx
//
// NOTE: Session recreation via RecreateSession may not work properly across worker fail/crash before Temporal server
// version v1.15.1.
func CreateSession(ctx Context, sessionOptions *SessionOptions) (Context, error) {
	return internal.CreateSession(ctx, sessionOptions)
}

// RecreateSession recreate a session based on the sessionInfo passed in. Activities executed within
// the recreated session will be executed by the same worker as the previous session. RecreateSession()
// returns an error under the same situation as CreateSession() or the token passed in is invalid.
// It also has the same usage as CreateSession().
//
// The main usage of RecreateSession is for long sessions that are splited into multiple runs. At the end of
// one run, complete the current session, get recreateToken from sessionInfo by calling SessionInfo.GetRecreateToken()
// and pass the token to the next run. In the new run, session can be recreated using that token.
//
// NOTE: Session recreation via RecreateSession may not work properly across worker fail/crash before Temporal server
// version v1.15.1.
func RecreateSession(ctx Context, recreateToken []byte, sessionOptions *SessionOptions) (Context, error) {
	return internal.RecreateSession(ctx, recreateToken, sessionOptions)
}

// CompleteSession completes a session. It releases worker resources, so other sessions can be created.
// CompleteSession won't do anything if the context passed in doesn't contain any session information or the
// session has already completed or failed.
//
// After a session has completed, user can continue to use the context, but the activities will be scheduled
// on the normal taskQueue (as user specified in ActivityOptions) and may be picked up by another worker since
// it's not in a session.
//
// Due to internal logic, this call must be made in the same coroutine CreateSession/RecreateSession were
// called in.
func CompleteSession(ctx Context) {
	internal.CompleteSession(ctx)
}

// GetSessionInfo returns the sessionInfo stored in the context. If there are multiple sessions in the context,
// (for example, the same context is used to create, complete, create another session. Then user found that the
// session has failed, and created a new one on it), the most recent sessionInfo will be returned.
//
// This API will return nil if there's no sessionInfo in the context.
func GetSessionInfo(ctx Context) *SessionInfo {
	return internal.GetSessionInfo(ctx)
}
