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

package metrics

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/tally"
	"github.com/uber/tchannel-go/thrift"
	"go.temporal.io/temporal/.gen/go/temporal/workflowserviceclient"
	"go.temporal.io/temporal/.gen/go/temporal/workflowservicetest"
	s "go.temporal.io/temporal/.gen/go/shared"
	"go.uber.org/yarpc"
)

var (
	safeCharacters = []rune{'_'}

	sanitizeOptions = tally.SanitizeOptions{
		NameCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		KeyCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		ValueCharacters: tally.ValidCharacters{
			Ranges:     tally.AlphanumericRange,
			Characters: safeCharacters,
		},
		ReplacementCharacter: tally.DefaultReplacementCharacter,
	}
)

type testCase struct {
	serviceMethod    string
	callArgs         []interface{}
	mockReturns      []interface{}
	expectedCounters []string
}

func Test_Wrapper(t *testing.T) {
	ctx, _ := thrift.NewContext(time.Minute)
	tests := []testCase{
		// one case for each service call
		{"DeprecateDomain", []interface{}{ctx, &s.DeprecateDomainRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"DescribeDomain", []interface{}{ctx, &s.DescribeDomainRequest{}}, []interface{}{&s.DescribeDomainResponse{}, nil}, []string{CadenceRequest}},
		{"GetWorkflowExecutionHistory", []interface{}{ctx, &s.GetWorkflowExecutionHistoryRequest{}}, []interface{}{&s.GetWorkflowExecutionHistoryResponse{}, nil}, []string{CadenceRequest}},
		{"ListClosedWorkflowExecutions", []interface{}{ctx, &s.ListClosedWorkflowExecutionsRequest{}}, []interface{}{&s.ListClosedWorkflowExecutionsResponse{}, nil}, []string{CadenceRequest}},
		{"ListOpenWorkflowExecutions", []interface{}{ctx, &s.ListOpenWorkflowExecutionsRequest{}}, []interface{}{&s.ListOpenWorkflowExecutionsResponse{}, nil}, []string{CadenceRequest}},
		{"PollForActivityTask", []interface{}{ctx, &s.PollForActivityTaskRequest{}}, []interface{}{&s.PollForActivityTaskResponse{}, nil}, []string{CadenceRequest}},
		{"PollForDecisionTask", []interface{}{ctx, &s.PollForDecisionTaskRequest{}}, []interface{}{&s.PollForDecisionTaskResponse{}, nil}, []string{CadenceRequest}},
		{"RecordActivityTaskHeartbeat", []interface{}{ctx, &s.RecordActivityTaskHeartbeatRequest{}}, []interface{}{&s.RecordActivityTaskHeartbeatResponse{}, nil}, []string{CadenceRequest}},
		{"RegisterDomain", []interface{}{ctx, &s.RegisterDomainRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RequestCancelWorkflowExecution", []interface{}{ctx, &s.RequestCancelWorkflowExecutionRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskCanceled", []interface{}{ctx, &s.RespondActivityTaskCanceledRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskCompleted", []interface{}{ctx, &s.RespondActivityTaskCompletedRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskFailed", []interface{}{ctx, &s.RespondActivityTaskFailedRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskCanceledByID", []interface{}{ctx, &s.RespondActivityTaskCanceledByIDRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskCompletedByID", []interface{}{ctx, &s.RespondActivityTaskCompletedByIDRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondActivityTaskFailedByID", []interface{}{ctx, &s.RespondActivityTaskFailedByIDRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"RespondDecisionTaskCompleted", []interface{}{ctx, &s.RespondDecisionTaskCompletedRequest{}}, []interface{}{nil, nil}, []string{CadenceRequest}},
		{"SignalWorkflowExecution", []interface{}{ctx, &s.SignalWorkflowExecutionRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"StartWorkflowExecution", []interface{}{ctx, &s.StartWorkflowExecutionRequest{}}, []interface{}{&s.StartWorkflowExecutionResponse{}, nil}, []string{CadenceRequest}},
		{"TerminateWorkflowExecution", []interface{}{ctx, &s.TerminateWorkflowExecutionRequest{}}, []interface{}{nil}, []string{CadenceRequest}},
		{"ResetWorkflowExecution", []interface{}{ctx, &s.ResetWorkflowExecutionRequest{}}, []interface{}{&s.ResetWorkflowExecutionResponse{}, nil}, []string{CadenceRequest}},
		{"UpdateDomain", []interface{}{ctx, &s.UpdateDomainRequest{}}, []interface{}{&s.UpdateDomainResponse{}, nil}, []string{CadenceRequest}},
		// one case of invalid request
		{"PollForActivityTask", []interface{}{ctx, &s.PollForActivityTaskRequest{}}, []interface{}{nil, &s.EntityNotExistsError{}}, []string{CadenceRequest, CadenceInvalidRequest}},
		// one case of server error
		{"PollForActivityTask", []interface{}{ctx, &s.PollForActivityTaskRequest{}}, []interface{}{nil, &s.InternalServiceError{}}, []string{CadenceRequest, CadenceError}},
		{"QueryWorkflow", []interface{}{ctx, &s.QueryWorkflowRequest{}}, []interface{}{nil, &s.InternalServiceError{}}, []string{CadenceRequest, CadenceError}},
		{"RespondQueryTaskCompleted", []interface{}{ctx, &s.RespondQueryTaskCompletedRequest{}}, []interface{}{&s.InternalServiceError{}}, []string{CadenceRequest, CadenceError}},
	}

	// run each test twice - once with the regular scope, once with a sanitized metrics scope
	for _, test := range tests {
		runTest(t, test, newService, assertMetrics, fmt.Sprintf("%v_normal", test.serviceMethod))
		runTest(t, test, newPromService, assertPromMetrics, fmt.Sprintf("%v_prom_sanitized", test.serviceMethod))
	}
}

func runTest(
	t *testing.T,
	test testCase,
	serviceFunc func(*testing.T) (*workflowservicetest.MockClient, workflowserviceclient.Interface, io.Closer, *CapturingStatsReporter),
	validationFunc func(*testing.T, *CapturingStatsReporter, string, []string),
	name string,
) {
	t.Run(name, func(t *testing.T) {
		mockService, wrapperService, closer, reporter := serviceFunc(t)
		switch test.serviceMethod {
		case "DeprecateDomain":
			mockService.EXPECT().DeprecateDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "DescribeDomain":
			mockService.EXPECT().DescribeDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "GetWorkflowExecutionHistory":
			mockService.EXPECT().GetWorkflowExecutionHistory(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "ListClosedWorkflowExecutions":
			mockService.EXPECT().ListClosedWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "ListOpenWorkflowExecutions":
			mockService.EXPECT().ListOpenWorkflowExecutions(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "PollForActivityTask":
			mockService.EXPECT().PollForActivityTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "PollForDecisionTask":
			mockService.EXPECT().PollForDecisionTask(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RecordActivityTaskHeartbeat":
			mockService.EXPECT().RecordActivityTaskHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RecordActivityTaskHeartbeatByID":
			mockService.EXPECT().RecordActivityTaskHeartbeatByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RegisterDomain":
			mockService.EXPECT().RegisterDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RequestCancelWorkflowExecution":
			mockService.EXPECT().RequestCancelWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskCanceled":
			mockService.EXPECT().RespondActivityTaskCanceled(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskCompleted":
			mockService.EXPECT().RespondActivityTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskFailed":
			mockService.EXPECT().RespondActivityTaskFailed(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskCanceledByID":
			mockService.EXPECT().RespondActivityTaskCanceledByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskCompletedByID":
			mockService.EXPECT().RespondActivityTaskCompletedByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondActivityTaskFailedByID":
			mockService.EXPECT().RespondActivityTaskFailedByID(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondDecisionTaskCompleted":
			mockService.EXPECT().RespondDecisionTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "SignalWorkflowExecution":
			mockService.EXPECT().SignalWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "SignaWithStartlWorkflowExecution":
			mockService.EXPECT().SignalWithStartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "StartWorkflowExecution":
			mockService.EXPECT().StartWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "TerminateWorkflowExecution":
			mockService.EXPECT().TerminateWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "ResetWorkflowExecution":
			mockService.EXPECT().ResetWorkflowExecution(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "UpdateDomain":
			mockService.EXPECT().UpdateDomain(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "QueryWorkflow":
			mockService.EXPECT().QueryWorkflow(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		case "RespondQueryTaskCompleted":
			mockService.EXPECT().RespondQueryTaskCompleted(gomock.Any(), gomock.Any(), gomock.Any()).Return(test.mockReturns...)
		}

		callOption := yarpc.CallOption{}
		inputs := make([]reflect.Value, len(test.callArgs))
		for i, arg := range test.callArgs {
			inputs[i] = reflect.ValueOf(arg)
		}
		inputs = append(inputs, reflect.ValueOf(callOption))
		method := reflect.ValueOf(wrapperService).MethodByName(test.serviceMethod)
		method.Call(inputs)
		closer.Close()
		validationFunc(t, reporter, test.serviceMethod, test.expectedCounters)
	})
}

func assertMetrics(t *testing.T, reporter *CapturingStatsReporter, methodName string, counterNames []string) {
	require.Equal(t, len(counterNames), len(reporter.counts))
	for _, name := range counterNames {
		counterName := CadenceMetricsPrefix + methodName + "." + name
		find := false
		// counters are not in order
		for _, counter := range reporter.counts {
			if counterName == counter.name {
				find = true
				break
			}
		}
		require.True(t, find)
	}
	require.Equal(t, 1, len(reporter.timers))
	require.Equal(t, CadenceMetricsPrefix+methodName+"."+CadenceLatency, reporter.timers[0].name)
}

func assertPromMetrics(t *testing.T, reporter *CapturingStatsReporter, methodName string, counterNames []string) {
	require.Equal(t, len(counterNames), len(reporter.counts))
	for _, name := range counterNames {
		counterName := makePromCompatible(CadenceMetricsPrefix + methodName + "." + name)
		find := false
		// counters are not in order
		for _, counter := range reporter.counts {
			if counterName == counter.name {
				find = true
				break
			}
		}
		require.True(t, find)
	}
	require.Equal(t, 1, len(reporter.timers))
	expected := makePromCompatible(CadenceMetricsPrefix + methodName + "." + CadenceLatency)
	require.Equal(t, expected, reporter.timers[0].name)
}

func makePromCompatible(name string) string {
	name = strings.Replace(name, "-", "_", -1)
	name = strings.Replace(name, ".", "_", -1)
	return name
}

func newService(t *testing.T) (
	mockService *workflowservicetest.MockClient,
	wrapperService workflowserviceclient.Interface,
	closer io.Closer,
	reporter *CapturingStatsReporter,
) {
	mockCtrl := gomock.NewController(t)
	mockService = workflowservicetest.NewMockClient(mockCtrl)
	isReplay := false
	scope, closer, reporter := NewMetricsScope(&isReplay)
	wrapperService = NewWorkflowServiceWrapper(mockService, scope)
	return
}

func newPromService(t *testing.T) (
	mockService *workflowservicetest.MockClient,
	wrapperService workflowserviceclient.Interface,
	closer io.Closer,
	reporter *CapturingStatsReporter,
) {
	mockCtrl := gomock.NewController(t)
	mockService = workflowservicetest.NewMockClient(mockCtrl)
	isReplay := false
	scope, closer, reporter := newPromScope(&isReplay)
	wrapperService = NewWorkflowServiceWrapper(mockService, scope)
	return
}

func newPromScope(isReplay *bool) (tally.Scope, io.Closer, *CapturingStatsReporter) {
	reporter := &CapturingStatsReporter{}
	opts := tally.ScopeOptions{
		Reporter:        reporter,
		SanitizeOptions: &sanitizeOptions,
	}
	scope, closer := tally.NewRootScope(opts, time.Second)
	return WrapScope(isReplay, scope, &realClock{}), closer, reporter
}
