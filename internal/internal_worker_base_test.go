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

type emptyTask struct{}

func newEmptyTask() *emptyTask {
	return &emptyTask{}
}

// isEmpty implements taskForWorker.
func (t *emptyTask) isEmpty() bool {
	return true
}

// scaleDecision implements taskForWorker.
func (t *emptyTask) scaleDecision() (pollerScaleDecision, bool) {
	return pollerScaleDecision{}, false
}

func (s *PollScalerReportHandleSuite) TestErrorScaleDown() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(0))
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 4, targetSuggestion, "should suggest scaling down on resource exhausted error")
	// Non resource exhausted errors should scale down by 1
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 3, targetSuggestion)
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 2, targetSuggestion)
	// We should not scale down below minPollerCount
	ps.handleError(serviceerror.NewInternal("test error"))
	assert.Equal(s.T(), 2, targetSuggestion)
	ps.handleError(serviceerror.NewResourceExhausted(enumspb.RESOURCE_EXHAUSTED_CAUSE_CONCURRENT_LIMIT, ""))
	assert.Equal(s.T(), 2, targetSuggestion)
}

func (s *PollScalerReportHandleSuite) TestScaleDownOnEmptyTask() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(0))
	ps.handleTask(newEmptyTask())
	assert.Equal(s.T(), 7, targetSuggestion)
}

func (s *PollScalerReportHandleSuite) TestScaleUpOnDelay() {
	targetSuggestion := 0
	ps := newPollScalerReportHandle(pollScalerReportHandleOptions{
		initialPollerCount: 8,
		maxPollerCount:     10,
		minPollerCount:     2,
		scaleCallback: func(suggestion int) {
			targetSuggestion = suggestion
		},
	})
	ps.handleTask(newTestTask(10))
	assert.Equal(s.T(), 0, targetSuggestion)
	ps.newPeriod()
	ps.handleTask(newTestTask(100))
	// We should scale up to but not past the max poller count
	assert.Equal(s.T(), 10, targetSuggestion)

}
