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

package mocks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	eventpb "go.temporal.io/temporal-proto/event"
	filterpb "go.temporal.io/temporal-proto/filter"

	"go.temporal.io/temporal/client"
	"go.temporal.io/temporal/workflow"
)

func Test_MockClient(t *testing.T) {
	testWorkflowID := "test-workflowid"
	testRunID := "test-runid"
	testWorkflowName := "workflow"
	testWorkflowInput := "input"

	mockClient := &Client{}

	mockClient.On("StartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&workflow.Execution{ID: testWorkflowID, RunID: testRunID}, nil).Once()
	we, err := mockClient.StartWorkflow(context.Background(), client.StartWorkflowOptions{}, testWorkflowName, testWorkflowInput)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, we.ID)
	require.Equal(t, testRunID, we.RunID)

	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&workflow.Execution{ID: testWorkflowID, RunID: testRunID}, nil).Once()
	we, err = mockClient.SignalWithStartWorkflow(context.Background(), "wid", "signal", "val", client.StartWorkflowOptions{}, testWorkflowName, testWorkflowInput)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, we.ID)
	require.Equal(t, testRunID, we.RunID)

	mockWfRun := &WorkflowRun{}
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mockWfRun, nil).Once()
	wfRun, err := mockClient.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{}, testWorkflowName, testWorkflowInput)
	mockClient.AssertExpectations(t)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, we.ID)
	require.Equal(t, testRunID, we.RunID)

	mockWfRun.On("GetID").Return(testWorkflowID).Once()
	mockWfRun.On("GetRunID").Return(testRunID).Once()
	mockWfRun.On("Get", mock.Anything, mock.Anything).Return(nil).Once()
	require.Equal(t, testWorkflowID, wfRun.GetID())
	require.Equal(t, testRunID, wfRun.GetRunID())
	require.NoError(t, wfRun.Get(context.Background(), &testWorkflowID))

	mockWfRun.On("GetID").Return(testWorkflowID).Once()
	mockWfRun.On("GetRunID").Return(testRunID).Once()
	mockWfRun.On("Get", mock.Anything, mock.Anything).Return(nil).Once()
	mockClient.On("GetWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(mockWfRun).Once()
	wfRun = mockClient.GetWorkflow(context.Background(), testWorkflowID, testRunID)
	mockClient.AssertExpectations(t)
	require.Equal(t, testWorkflowID, wfRun.GetID())
	require.Equal(t, testRunID, wfRun.GetRunID())
	require.NoError(t, wfRun.Get(context.Background(), &testWorkflowID))

	mockHistoryIter := &HistoryEventIterator{}
	mockHistoryIter.On("HasNext").Return(true).Once()
	mockHistoryIter.On("Next").Return(&eventpb.HistoryEvent{}, nil).Once()
	mockClient.On("GetWorkflowHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(mockHistoryIter).Once()
	historyIter := mockClient.GetWorkflowHistory(context.Background(), testWorkflowID, testRunID, true, filterpb.HistoryEventFilterTypeCloseEvent)
	mockClient.AssertExpectations(t)
	require.NotNil(t, historyIter)
	require.Equal(t, true, historyIter.HasNext())
	next, err := historyIter.Next()
	require.NotNil(t, next)
	require.NoError(t, err)
}
