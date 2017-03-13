package cadence

// All code in this file is private to the package.

import (
	"fmt"
	"os"
	"sync"
	"time"

	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
)

// GetWorkerIdentity gets a default identity for the worker.
func getWorkerIdentity(tasklistName string) string {
	hostName, err := os.Hostname()
	if err != nil {
		hostName = "UnKnown"
	}
	return fmt.Sprintf("%d@%s@%s", os.Getpid(), hostName, tasklistName)
}

func flowActivityTypeFrom(v s.ActivityType) ActivityType {
	return ActivityType{Name: v.GetName()}
}

// ActivityTypePtr makes a copy and returns the pointer to a ActivityType.
func activityTypePtr(v ActivityType) *s.ActivityType {
	return &s.ActivityType{Name: common.StringPtr(v.Name)}
}

func flowWorkflowTypeFrom(v s.WorkflowType) WorkflowType {
	return WorkflowType{Name: v.GetName()}
}

// WorkflowTypePtr makes a copy and returns the pointer to a WorkflowType.
func workflowTypePtr(t WorkflowType) *s.WorkflowType {
	return &s.WorkflowType{Name: common.StringPtr(t.Name)}
}

// workflowExecutionPtr makes a copy and returns the pointer to a WorkflowExecution.
func workflowExecutionPtr(t WorkflowExecution) *s.WorkflowExecution {
	return &s.WorkflowExecution{
		WorkflowId: common.StringPtr(t.ID),
		RunId:      common.StringPtr(t.RunID),
	}
}

// getErrorDetails gets reason and details.
func getErrorDetails(err error) (string, []byte) {
	if wErr, ok := err.(Error); ok {
		return wErr.Reason(), wErr.Details()
	}
	if wErr, ok := err.(CanceledError); ok {
		return "canceled", wErr.Details()
	}
	return err.Error(), []byte("")
}

// AwaitWaitGroup calls Wait on the given wait
// Returns true if the Wait() call succeeded before the timeout
// Returns false if the Wait() did not return before the timeout
func awaitWaitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	doneC := make(chan struct{})

	go func() {
		wg.Wait()
		close(doneC)
	}()

	select {
	case <-doneC:
		return true
	case <-time.After(timeout):
		return false
	}
}
