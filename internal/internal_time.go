package internal

import (
	"time"
)

// All code in this file is private to the package.

type (
	timerInfo struct {
		timerID string
	}

	// workflowTimerClient wraps the async workflow timer functionality.
	workflowTimerClient interface {

		// Now - Current time when the decision task is started or replayed.
		// the workflow need to use this for wall clock to make the flow logic deterministic.
		Now() time.Time

		// NewTimer - Creates a new timer that will fire callback after d(resolution is in seconds).
		// The callback indicates the error(TimerCanceledError) if the timer is cancelled.
		NewTimer(d time.Duration, callback resultHandler) *timerInfo

		// RequestCancelTimer - Requests cancel of a timer, this one doesn't wait for cancellation request
		// to complete, instead invokes the resultHandler with TimerCanceledError
		// If the timer is not started then it is a no-operation.
		RequestCancelTimer(timerID string)
	}
)
