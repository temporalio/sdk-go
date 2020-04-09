package test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.temporal.io/temporal"
	"go.temporal.io/temporal/activity"
	"go.temporal.io/temporal/worker"
)

type Activities struct {
	mu          sync.Mutex
	invocations []string
	activities2 *Activities2
}

type Activities2 struct {
	impl *Activities
}

var errFailOnPurpose = temporal.NewCustomError("failing-on-purpose")

func newActivities() *Activities {
	activities2 := &Activities2{}
	result := &Activities{activities2: activities2}
	activities2.impl = result
	return result
}

func (a *Activities) RetryTimeoutStableErrorActivity() error {
	time.Sleep(time.Second * 3)
	return errFailOnPurpose
}

func (a *Activities) Sleep(_ context.Context, delay time.Duration) error {
	a.append("sleep")
	time.Sleep(delay)
	return nil
}

func LocalSleep(_ context.Context, delay time.Duration) error {
	time.Sleep(delay)
	return nil
}

func (a *Activities) HeartbeatAndSleep(ctx context.Context, seq int, delay time.Duration) (int, error) {
	a.append("heartbeatAndSleep")
	if activity.HasHeartbeatDetails(ctx) {
		var prev int
		if err := activity.GetHeartbeatDetails(ctx, &prev); err == nil {
			seq = prev
		}
	}
	seq++
	activity.RecordHeartbeat(ctx, seq)
	time.Sleep(delay)
	return seq, nil
}

func (a *Activities) fail(_ context.Context) error {
	a.append("fail")
	return errFailOnPurpose
}

func (a *Activities) InspectActivityInfo(ctx context.Context, namespace, taskList, wfType string) error {
	a.append("inspectActivityInfo")
	info := activity.GetInfo(ctx)
	if info.WorkflowNamespace != namespace {
		return fmt.Errorf("expected namespace %v but got %v", namespace, info.WorkflowNamespace)
	}
	if info.WorkflowType == nil || info.WorkflowType.Name != wfType {
		return fmt.Errorf("expected workflowType %v but got %v", wfType, info.WorkflowType)
	}
	if info.TaskList != taskList {
		return fmt.Errorf("expected taskList %v but got %v", taskList, info.TaskList)
	}
	return nil
}

func (a *Activities) append(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = append(a.invocations, name)
}

func (a *Activities) invoked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, len(a.invocations))
	for i := range a.invocations {
		result[i] = a.invocations[i]
	}
	return result
}

func (a *Activities) clearInvoked() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invocations = []string{}
}

func (a *Activities2) ToUpper(_ context.Context, arg string) (string, error) {
	a.impl.append("toUpper")
	return strings.ToUpper(arg), nil
}

func (a *Activities2) ToUpperWithDelay(_ context.Context, arg string, delay time.Duration) (string, error) {
	a.impl.append("toUpperWithDelay")
	time.Sleep(delay)
	return strings.ToUpper(arg), nil
}

func (a *Activities) GetMemoAndSearchAttr(_ context.Context, memo, searchAttr string) (string, error) {
	a.append("getMemoAndSearchAttr")
	return memo + ", " + searchAttr, nil
}

func (a *Activities) register(worker worker.Worker) {
	worker.RegisterActivity(a)
	// Check reregistration
	worker.RegisterActivityWithOptions(a.fail, activity.RegisterOptions{Name: "Fail", DisableAlreadyRegisteredCheck: true})
	// Check prefix
	worker.RegisterActivityWithOptions(a.activities2, activity.RegisterOptions{Name: "Prefix_", DisableAlreadyRegisteredCheck: true})
	worker.RegisterActivityWithOptions(a.InspectActivityInfo, activity.RegisterOptions{Name: "inspectActivityInfo"})
}
