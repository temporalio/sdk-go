package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
)

type (
	PollScalerReportHandleSuite struct {
		suite.Suite
	}
)

func TestPollScalerReportHandleSuite(t *testing.T) {
	suite.Run(t, new(PollScalerReportHandleSuite))
}

type testTask struct {
	psd pollerScaleDecision
}

// isEmpty implements taskForWorker.
func (t *testTask) isEmpty() bool {
	return false
}

// scaleDecision implements taskForWorker.
func (t *testTask) scaleDecision() (pollerScaleDecision, bool) {
	return t.psd, true
}

func newTestTask(delta int) *testTask {
	return &testTask{
		psd: pollerScaleDecision{
			pollRequestDeltaSuggestion: delta,
		},
	}
}

func (s *PollScalerReportHandleSuite) TestResourceExhausted() {
	suggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 4,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(delta int) {
			suggestion = delta
		},
	})
	ps.handleTask(newTestTask(0))
	ps.newPeriod()
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 2, suggestion, "should not suggest scaling down on resource exhausted error")

}
