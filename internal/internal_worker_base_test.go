package internal

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type (
	PollScalerReportHandleSuite struct {
		suite.Suite
	}
)

func TestPollScalerReportHandleSuite(t *testing.T) {
	suite.Run(t, new(PollScalerReportHandleSuite))
}

func (s *PollScalerReportHandleSuite) TestCreateAndFree() {

}
