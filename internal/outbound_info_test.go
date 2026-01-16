package internal

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

// outboundInfoCapturingDataConverter captures OutboundInfo from workflow context during serialization
type outboundInfoCapturingDataConverter struct {
	converter.DataConverter
	mu            sync.Mutex
	capturedInfos []*OutboundInfo
}

func newOutboundInfoCapturingDataConverter() *outboundInfoCapturingDataConverter {
	return &outboundInfoCapturingDataConverter{
		DataConverter: converter.GetDefaultDataConverter(),
		capturedInfos: make([]*OutboundInfo, 0),
	}
}

func (dc *outboundInfoCapturingDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	info := GetOutboundInfo(ctx)
	if info != nil {
		infoCopy := *info
		dc.capturedInfos = append(dc.capturedInfos, &infoCopy)
	}
	return dc
}

func (dc *outboundInfoCapturingDataConverter) WithContext(_ context.Context) converter.DataConverter {
	return dc
}

func (dc *outboundInfoCapturingDataConverter) getCapturedInfos() []*OutboundInfo {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	result := make([]*OutboundInfo, len(dc.capturedInfos))
	copy(result, dc.capturedInfos)
	return result
}

func (dc *outboundInfoCapturingDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	return dc.DataConverter.ToPayload(value)
}

func (dc *outboundInfoCapturingDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	return dc.DataConverter.ToPayloads(values...)
}

func (dc *outboundInfoCapturingDataConverter) FromPayload(payload *commonpb.Payload, valuePtr interface{}) error {
	return dc.DataConverter.FromPayload(payload, valuePtr)
}

func (dc *outboundInfoCapturingDataConverter) FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error {
	return dc.DataConverter.FromPayloads(payloads, valuePtrs...)
}

func (dc *outboundInfoCapturingDataConverter) ToString(payload *commonpb.Payload) string {
	return dc.DataConverter.ToString(payload)
}

func (dc *outboundInfoCapturingDataConverter) ToStrings(payloads *commonpb.Payloads) []string {
	return dc.DataConverter.ToStrings(payloads)
}

type OutboundInfoTestSuite struct {
	suite.Suite
	WorkflowTestSuite
}

func TestOutboundInfoTestSuite(t *testing.T) {
	suite.Run(t, new(OutboundInfoTestSuite))
}

func simpleActivity(_ string) (string, error) {
	return "done", nil
}

func (s *OutboundInfoTestSuite) TestActivityOutboundInfo() {
	dc := newOutboundInfoCapturingDataConverter()

	env := s.NewTestWorkflowEnvironment()
	env.SetDataConverter(dc)
	env.RegisterActivity(simpleActivity)

	workflow := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		var result string
		err := ExecuteActivity(ctx, simpleActivity, "input").Get(ctx, &result)
		return err
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	capturedInfos := dc.getCapturedInfos()
	s.NotEmpty(capturedInfos, "should have captured OutboundInfo during activity input serialization")

	foundActivityID := false
	for _, info := range capturedInfos {
		if info.ActivityID == "1" {
			foundActivityID = true
			break
		}
	}
	s.True(foundActivityID, "should have captured ActivityID in OutboundInfo")
}

func (s *OutboundInfoTestSuite) TestActivityWithExplicitID() {
	dc := newOutboundInfoCapturingDataConverter()

	env := s.NewTestWorkflowEnvironment()
	env.SetDataConverter(dc)
	env.RegisterActivity(simpleActivity)

	explicitActivityID := "my-explicit-activity-id"
	workflow := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Minute,
			ActivityID:          explicitActivityID,
		})
		var result string
		err := ExecuteActivity(ctx, simpleActivity, "input").Get(ctx, &result)
		return err
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	capturedInfos := dc.getCapturedInfos()
	s.NotEmpty(capturedInfos)

	// Verify the explicit activity ID was captured
	foundExplicitID := false
	for _, info := range capturedInfos {
		if info.ActivityID == explicitActivityID {
			foundExplicitID = true
			break
		}
	}
	s.True(foundExplicitID, "should have captured the explicit ActivityID")
}

func childWorkflowForTest(_ Context, input string) (string, error) {
	return "child-" + input, nil
}

func (s *OutboundInfoTestSuite) TestChildWorkflowOutboundInfo() {
	dc := newOutboundInfoCapturingDataConverter()

	env := s.NewTestWorkflowEnvironment()
	env.SetDataConverter(dc)
	env.RegisterWorkflow(childWorkflowForTest)

	workflow := func(ctx Context) error {
		cwo := ChildWorkflowOptions{
			WorkflowRunTimeout: time.Minute,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var result string
		err := ExecuteChildWorkflow(ctx, childWorkflowForTest, "input").Get(ctx, &result)
		return err
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	capturedInfos := dc.getCapturedInfos()
	s.NotEmpty(capturedInfos, "should have captured OutboundInfo during child workflow input serialization")

	// Verify at least one capture has ChildWorkflowID set
	foundChildWorkflowID := false
	for _, info := range capturedInfos {
		if info.ChildWorkflowID != "" {
			foundChildWorkflowID = true
			break
		}
	}
	s.True(foundChildWorkflowID, "should have captured ChildWorkflowID in OutboundInfo")
}

func (s *OutboundInfoTestSuite) TestChildWorkflowWithExplicitID() {
	dc := newOutboundInfoCapturingDataConverter()

	env := s.NewTestWorkflowEnvironment()
	env.SetDataConverter(dc)
	env.RegisterWorkflow(childWorkflowForTest)

	explicitChildID := "my-explicit-child-id"
	workflow := func(ctx Context) error {
		cwo := ChildWorkflowOptions{
			WorkflowID:         explicitChildID,
			WorkflowRunTimeout: time.Minute,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var result string
		err := ExecuteChildWorkflow(ctx, childWorkflowForTest, "input").Get(ctx, &result)
		return err
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	capturedInfos := dc.getCapturedInfos()
	s.NotEmpty(capturedInfos)

	// Verify the explicit child workflow ID was captured
	foundExplicitID := false
	for _, info := range capturedInfos {
		if info.ChildWorkflowID == explicitChildID {
			foundExplicitID = true
			break
		}
	}
	s.True(foundExplicitID, "should have captured the explicit ChildWorkflowID")
}
