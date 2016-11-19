package common

import (
	m "code.uber.internal/devexp/minions-client-go.git/.gen/go/minions"
)

// StringPtr makes a copy and returns the pointer to a string.
func StringPtr(v string) *string {
	return &v
}

// TaskListPtr makes a copy and returns the pointer to a TaskList.
func TaskListPtr(v m.TaskList) *m.TaskList {
	return &v
}
