package flow

import (
	"fmt"
	"os"

	s "code.uber.internal/devexp/minions-client-go.git/.gen/go/shared"
	"code.uber.internal/devexp/minions-client-go.git/common"
)

// GetWorkerIdentity gets a default identity for the worker.
func GetWorkerIdentity(tasklistName string) string {
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
func ActivityTypePtr(v ActivityType) *s.ActivityType {
	return &s.ActivityType{Name: common.StringPtr(v.Name)}
}

func flowWorkflowTypeFrom(v s.WorkflowType) WorkflowType {
	return WorkflowType{Name: v.GetName()}
}

// WorkflowTypePtr makes a copy and returns the pointer to a WorkflowType.
func WorkflowTypePtr(t WorkflowType) *s.WorkflowType {
	return &s.WorkflowType{Name: common.StringPtr(t.Name)}
}
