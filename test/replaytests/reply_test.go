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

package replaytests

import (
	"context"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/api/workflowservicemock/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/worker"
)

type replayTestSuite struct {
	suite.Suite
	mockCtrl *gomock.Controller
	service  *workflowservicemock.MockWorkflowServiceClient
}

func TestReplayTestSuite(t *testing.T) {
	s := new(replayTestSuite)
	suite.Run(t, s)
}

func (s *replayTestSuite) SetupTest() {
	s.mockCtrl = gomock.NewController(s.T())
	s.service = workflowservicemock.NewMockWorkflowServiceClient(s.mockCtrl)
}

func (s *replayTestSuite) TearDownTest() {
	s.mockCtrl.Finish() // assert mock’s expectations
}

func (s *replayTestSuite) TestGenerateWorkflowHistory() {
	s.T().Skip("Remove this Skip to regenerate the history.")
	c, _ := client.NewClient(client.Options{
		Logger: log.NewDefaultLogger(),
	})
	defer c.Close()

	w := worker.New(c, "replay-test", worker.Options{})

	w.RegisterWorkflow(Workflow1)
	w.RegisterWorkflow(Workflow2)
	w.RegisterActivity(helloworldActivity)

	_ = w.Start()
	defer w.Stop()

	workflowOptions1 := client.StartWorkflowOptions{
		ID:        "replay-tests-workflow1",
		TaskQueue: "replay-test",
	}
	we1, _ := c.ExecuteWorkflow(context.Background(), workflowOptions1, Workflow1, "Workflow1")
	var res1 string
	_ = we1.Get(context.Background(), &res1)

	workflowOptions2 := client.StartWorkflowOptions{
		ID:        "replay-tests-workflow2",
		TaskQueue: "replay-test",
	}
	we2, _ := c.ExecuteWorkflow(context.Background(), workflowOptions2, Workflow2, "Workflow2")
	var res2 string
	_ = we2.Get(context.Background(), &res2)

	// Now run:
	// tctl workflow show --workflow_id replay-tests-workflow1 --of workflow1.json
	// tctl workflow show --workflow_id replay-tests-workflow2 --of workflow2.json
}

func (s *replayTestSuite) TestReplayWorkflowHistoryFromFile() {
	testFiles := []string{"workflow1.json", "workflow2.json"}
	var err error

	for _, testFile := range testFiles {
		replayer := worker.NewWorkflowReplayer()
		replayer.RegisterWorkflow(Workflow1)
		replayer.RegisterWorkflow(Workflow2)

		err = replayer.ReplayWorkflowHistoryFromJSONFile(log.NewDefaultLogger(), testFile)
		require.NoError(s.T(), err, "file: %s", testFile)
	}
}

func (s *replayTestSuite) TestReplayBadWorkflowHistoryFromFile() {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(Workflow1)

	err := replayer.ReplayWorkflowHistoryFromJSONFile(log.NewDefaultLogger(), "bad-history.json")
	require.Error(s.T(), err)
	require.True(s.T(), strings.HasPrefix(err.Error(), "replay workflow failed with failure"))
}
