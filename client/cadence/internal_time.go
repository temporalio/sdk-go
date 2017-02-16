package cadence

import "time"

// All code in this file is private to the package.

type (

	// workflowTimerClient wraps the async workflow timer functionality.
	workflowTimerClient interface {

		// Now - Current time when the decision task is started or replayed.
		// the workflow need to use this for wall clock to make the flow logic deterministic.
		Now() time.Time

		// NewTimer - Creates a new timer that will fire callback after d(resolution is in seconds).
		NewTimer(d time.Duration, callback resultHandler)
	}
)
