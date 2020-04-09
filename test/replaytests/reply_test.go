package replaytests

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"go.temporal.io/temporal-proto/workflowservicemock"

	"go.temporal.io/temporal/worker"

	"go.uber.org/zap"
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

func (s *replayTestSuite) TestReplayWorkflowHistoryFromFile() {
	logger, _ := zap.NewDevelopment()
	testFiles := []string{"basic.json", "basic_new.json", "version.json", "version_new.json"}
	var err error

	for _, testFile := range testFiles {
		replayer := worker.NewWorkflowReplayer()
		replayer.RegisterWorkflow(Workflow)
		replayer.RegisterWorkflow(Workflow2)

		err = replayer.ReplayWorkflowHistoryFromJSONFile(logger, testFile)
		require.NoError(s.T(), err)
	}
}
