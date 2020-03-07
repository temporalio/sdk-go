// Copyright (c) 2017 Uber Technologies, Inc.
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

package test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/cadence"
	"go.uber.org/cadence/activity"
)

type Activities struct {
	sync.Mutex
	invocations []string
}

var errFailOnPurpose = cadence.NewCustomError("failing-on-purpose")

func (a *Activities) RetryTimeoutStableErrorActivity(ctx context.Context) error {
	time.Sleep(time.Second * 3)
	return errFailOnPurpose
}

func (a *Activities) Sleep(ctx context.Context, delay time.Duration) error {
	a.append("sleep")
	time.Sleep(delay)
	return nil
}

func LocalSleep(ctx context.Context, delay time.Duration) error {
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

func (a *Activities) ToUpper(ctx context.Context, arg string) (string, error) {
	a.append("toUpper")
	return strings.ToUpper(arg), nil
}

func (a *Activities) ToUpperWithDelay(ctx context.Context, arg string, delay time.Duration) (string, error) {
	a.append("toUpperWithDelay")
	time.Sleep(delay)
	return strings.ToUpper(arg), nil
}

func (a *Activities) GetMemoAndSearchAttr(ctx context.Context, memo, searchAttr string) (string, error) {
	a.append("getMemoAndSearchAttr")
	return memo + ", " + searchAttr, nil
}

func (a *Activities) Fail(ctx context.Context) error {
	a.append("fail")
	return errFailOnPurpose
}

func (a *Activities) InspectActivityInfo(ctx context.Context, domain, taskList, wfType string) error {
	a.append("inspectActivityInfo")
	info := activity.GetInfo(ctx)
	if info.WorkflowDomain != domain {
		return fmt.Errorf("expected domainName %v but got %v", domain, info.WorkflowDomain)
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
	a.Lock()
	defer a.Unlock()
	a.invocations = append(a.invocations, name)
}

func (a *Activities) invoked() []string {
	a.Lock()
	defer a.Unlock()
	result := make([]string, len(a.invocations))
	for i := range a.invocations {
		result[i] = a.invocations[i]
	}
	return result
}

func (a *Activities) clearInvoked() {
	a.Lock()
	defer a.Unlock()
	a.invocations = []string{}
}

func (a *Activities) register() {
	activity.RegisterWithOptions(a.Fail, activity.RegisterOptions{Name: "fail"})
	activity.RegisterWithOptions(a.Sleep, activity.RegisterOptions{Name: "sleep"})
	activity.RegisterWithOptions(a.ToUpper, activity.RegisterOptions{Name: "toUpper"})
	activity.RegisterWithOptions(a.ToUpperWithDelay, activity.RegisterOptions{Name: "toUpperWithDelay"})
	activity.RegisterWithOptions(a.HeartbeatAndSleep, activity.RegisterOptions{Name: "heartbeatAndSleep"})
	activity.RegisterWithOptions(a.GetMemoAndSearchAttr, activity.RegisterOptions{Name: "getMemoAndSearchAttr"})
	activity.RegisterWithOptions(a.RetryTimeoutStableErrorActivity, activity.RegisterOptions{Name: "retryTimeoutStableErrorActivity"})
	activity.RegisterWithOptions(a.InspectActivityInfo, activity.RegisterOptions{Name: "inspectActivityInfo"})
}
