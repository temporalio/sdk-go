package examples

import (
	"testing"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/suite"
)

type (
	GreetingsWorkflowTestSuite struct {
		suite.Suite
	}
)

// Test suite.
func (s *GreetingsWorkflowTestSuite) SetupTest() {
}

func TestGreetingsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(GreetingsWorkflowTestSuite))
}

// TODO: The test need to be written against the workflow layer.
func TestWorkflow(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
}
