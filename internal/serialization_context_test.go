package internal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
)

// serCtxSigningCodec adds a context-derived signature on Encode and verifies on Decode.
type serCtxSigningCodec struct {
	signature string
}

func (c *serCtxSigningCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	switch sc := ctx.(type) {
	case converter.WorkflowSerializationContext:
		return &serCtxSigningCodec{signature: sc.WorkflowID}
	case converter.ActivitySerializationContext:
		return &serCtxSigningCodec{signature: sc.WorkflowID + ":" + sc.ActivityType}
	}
	return c
}

func (c *serCtxSigningCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		clone := proto.Clone(p).(*commonpb.Payload)
		if clone.Metadata == nil {
			clone.Metadata = map[string][]byte{}
		}
		clone.Metadata["ctx-signature"] = []byte(c.signature)
		result[i] = clone
	}
	return result, nil
}

func (c *serCtxSigningCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		sig := string(p.Metadata["ctx-signature"])
		if sig != c.signature {
			return nil, fmt.Errorf("signature mismatch: got %q, want %q", sig, c.signature)
		}
		clone := proto.Clone(p).(*commonpb.Payload)
		delete(clone.Metadata, "ctx-signature")
		result[i] = clone
	}
	return result, nil
}

// serCtxCapturingDataConverter records every SerializationContext it receives.
type serCtxCapturingDataConverter struct {
	converter.DataConverter
	mu       *sync.Mutex
	contexts *[]converter.SerializationContext
}

func newSerCtxCapturingDataConverter() *serCtxCapturingDataConverter {
	contexts := make([]converter.SerializationContext, 0)
	mu := &sync.Mutex{}
	return &serCtxCapturingDataConverter{
		DataConverter: converter.GetDefaultDataConverter(),
		mu:            mu,
		contexts:      &contexts,
	}
}

func (dc *serCtxCapturingDataConverter) WithSerializationContext(ctx converter.SerializationContext) converter.DataConverter {
	dc.mu.Lock()
	*dc.contexts = append(*dc.contexts, ctx)
	dc.mu.Unlock()
	return &serCtxCapturingDataConverter{
		DataConverter: dc.DataConverter,
		mu:            dc.mu,
		contexts:      dc.contexts,
	}
}

func (dc *serCtxCapturingDataConverter) getCapturedContexts() []converter.SerializationContext {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]converter.SerializationContext, len(*dc.contexts))
	copy(out, *dc.contexts)
	return out
}

// serCtxRequiredCodec fails encoding unless WithSerializationContext has been called with a non-empty context.
type serCtxRequiredCodec struct {
	hasContext bool
}

func (c *serCtxRequiredCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	return &serCtxRequiredCodec{hasContext: true}
}

func (c *serCtxRequiredCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if !c.hasContext {
		return nil, fmt.Errorf("serCtxRequiredCodec: Encode called without serialization context")
	}
	return payloads, nil
}

func (c *serCtxRequiredCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if !c.hasContext {
		return nil, fmt.Errorf("serCtxRequiredCodec: Decode called without serialization context")
	}
	return payloads, nil
}

// serCtxEncryptionCodec derives a deterministic key from context for concurrent isolation testing.
type serCtxEncryptionCodec struct {
	key string
}

func (c *serCtxEncryptionCodec) WithSerializationContext(ctx converter.SerializationContext) converter.PayloadCodec {
	switch sc := ctx.(type) {
	case converter.WorkflowSerializationContext:
		return &serCtxEncryptionCodec{key: "wf:" + sc.WorkflowID}
	case converter.ActivitySerializationContext:
		return &serCtxEncryptionCodec{key: "act:" + sc.WorkflowID + ":" + sc.ActivityType}
	}
	return c
}

func (c *serCtxEncryptionCodec) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		clone := proto.Clone(p).(*commonpb.Payload)
		if clone.Metadata == nil {
			clone.Metadata = map[string][]byte{}
		}
		clone.Metadata["encryption-key"] = []byte(c.key)
		result[i] = clone
	}
	return result, nil
}

func (c *serCtxEncryptionCodec) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		key := string(p.Metadata["encryption-key"])
		if key != c.key {
			return nil, fmt.Errorf("encryption key mismatch: got %q, want %q", key, c.key)
		}
		clone := proto.Clone(p).(*commonpb.Payload)
		delete(clone.Metadata, "encryption-key")
		result[i] = clone
	}
	return result, nil
}

type SerializationContextTestSuite struct {
	suite.Suite
	WorkflowTestSuite
}

func TestSerializationContextSuite(t *testing.T) {
	suite.Run(t, new(SerializationContextTestSuite))
}

func (s *SerializationContextTestSuite) TestActivityRoundTrip_SigningCodec() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	env.SetDataConverter(codecDC)

	activity := func(_ context.Context, input string) (string, error) {
		return strings.ToUpper(input), nil
	}
	env.RegisterActivity(activity)

	workflow := func(ctx Context) (string, error) {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		var result string
		err := ExecuteActivity(ctx, activity, "hello").Get(ctx, &result)
		return result, err
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("HELLO", result)
}

func (s *SerializationContextTestSuite) TestActivityRoundTrip_CapturingDC() {
	env := s.NewTestWorkflowEnvironment()
	dc := newSerCtxCapturingDataConverter()
	env.SetDataConverter(dc)

	activity := func(_ context.Context, input string) (string, error) {
		return input, nil
	}
	env.RegisterActivity(activity)

	workflow := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		var result string
		return ExecuteActivity(ctx, activity, "test").Get(ctx, &result)
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	captured := dc.getCapturedContexts()
	s.NotEmpty(captured)

	foundActivity := false
	for _, ctx := range captured {
		if actCtx, ok := ctx.(converter.ActivitySerializationContext); ok {
			foundActivity = true
			s.NotEmpty(actCtx.WorkflowID)
			s.NotEmpty(actCtx.ActivityType)
			s.False(actCtx.IsLocal)
			break
		}
	}
	s.True(foundActivity, "should have captured ActivitySerializationContext")
}

func (s *SerializationContextTestSuite) TestLocalActivityRoundTrip_CapturingDC() {
	env := s.NewTestWorkflowEnvironment()
	dc := newSerCtxCapturingDataConverter()
	env.SetDataConverter(dc)

	activity := func(_ context.Context, input string) (string, error) {
		return input, nil
	}
	env.RegisterActivity(activity)

	workflow := func(ctx Context) error {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		var result string
		return ExecuteLocalActivity(ctx, activity, "test").Get(ctx, &result)
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	captured := dc.getCapturedContexts()
	s.NotEmpty(captured)

	foundLocal := false
	for _, ctx := range captured {
		if actCtx, ok := ctx.(converter.ActivitySerializationContext); ok && actCtx.IsLocal {
			foundLocal = true
			s.NotEmpty(actCtx.WorkflowID)
			s.NotEmpty(actCtx.ActivityType)
			break
		}
	}
	s.True(foundLocal, "should have captured ActivitySerializationContext with IsLocal=true")
}

func (s *SerializationContextTestSuite) TestWorkflowInput_RequiresContext() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxRequiredCodec{})
	env.SetDataConverter(codecDC)

	workflow := func(ctx Context, input string) (string, error) {
		return "got:" + input, nil
	}

	env.ExecuteWorkflow(workflow, "hello-world")
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("got:hello-world", result)
}

func (s *SerializationContextTestSuite) TestChildWorkflowRoundTrip_SigningCodec() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	env.SetDataConverter(codecDC)

	childWf := func(ctx Context, input string) (string, error) {
		runes := []rune(input)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes), nil
	}
	env.RegisterWorkflow(childWf)

	parentWf := func(ctx Context) (string, error) {
		cwo := ChildWorkflowOptions{
			WorkflowID:         "child-signing-test",
			WorkflowRunTimeout: time.Minute,
		}
		ctx = WithChildWorkflowOptions(ctx, cwo)
		var result string
		err := ExecuteChildWorkflow(ctx, childWf, "abc").Get(ctx, &result)
		return result, err
	}

	env.ExecuteWorkflow(parentWf)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("cba", result)
}

func (s *SerializationContextTestSuite) TestSideEffect_SigningCodec() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	env.SetDataConverter(codecDC)

	workflow := func(ctx Context) (string, error) {
		var sideEffectResult string
		encoded := SideEffect(ctx, func(ctx Context) interface{} {
			return "side-effect-value"
		})
		err := encoded.Get(&sideEffectResult)
		if err != nil {
			return "", err
		}
		return sideEffectResult, nil
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("side-effect-value", result)
}

func (s *SerializationContextTestSuite) TestEncryptionCodec_ConcurrentOperations() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxEncryptionCodec{})
	env.SetDataConverter(codecDC)

	activityA := func(_ context.Context, input string) (string, error) {
		return "A:" + input, nil
	}
	activityB := func(_ context.Context, input string) (string, error) {
		return "B:" + input, nil
	}
	env.RegisterActivity(activityA)
	env.RegisterActivity(activityB)

	childWf := func(ctx Context, input string) (string, error) {
		return "child:" + input, nil
	}
	env.RegisterWorkflow(childWf)

	workflow := func(ctx Context) (string, error) {
		actCtx := WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		childCtx := WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
			WorkflowID:         "child-concurrent-test",
			WorkflowRunTimeout: time.Minute,
		})

		// Run activity and child workflow concurrently
		futureA := ExecuteActivity(actCtx, activityA, "input")
		futureChild := ExecuteChildWorkflow(childCtx, childWf, "input")

		var resultA, resultChild string
		if err := futureA.Get(ctx, &resultA); err != nil {
			return "", err
		}
		if err := futureChild.Get(ctx, &resultChild); err != nil {
			return "", err
		}

		return resultA + "|" + resultChild, nil
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	var result string
	s.NoError(env.GetWorkflowResult(&result))
	s.Equal("A:input|child:input", result)
}

func (s *SerializationContextTestSuite) TestSignalExternalWorkflow_CapturingDC() {
	env := s.NewTestWorkflowEnvironment()
	dc := newSerCtxCapturingDataConverter()
	env.SetDataConverter(dc)

	targetWorkflowID := "target-workflow-id"
	env.OnSignalExternalWorkflow(
		"default-test-namespace", targetWorkflowID, "", "test-signal", "signal-data",
	).Return(nil)

	workflow := func(ctx Context) error {
		return SignalExternalWorkflow(ctx, targetWorkflowID, "", "test-signal", "signal-data").Get(ctx, nil)
	}

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	captured := dc.getCapturedContexts()
	foundTargetCtx := false
	for _, ctx := range captured {
		if wfCtx, ok := ctx.(converter.WorkflowSerializationContext); ok {
			if wfCtx.WorkflowID == targetWorkflowID {
				foundTargetCtx = true
				break
			}
		}
	}
	s.True(foundTargetCtx, "should have captured WorkflowSerializationContext with target workflow ID")
}

func (s *SerializationContextTestSuite) TestQueryResult_CapturingDC() {
	env := s.NewTestWorkflowEnvironment()
	dc := newSerCtxCapturingDataConverter()
	env.SetDataConverter(dc)

	queryType := "test-query"
	workflow := func(ctx Context) error {
		err := SetQueryHandler(ctx, queryType, func() (string, error) {
			return "query-result", nil
		})
		if err != nil {
			return err
		}
		_ = Sleep(ctx, time.Minute)
		return nil
	}

	env.RegisterDelayedCallback(func() {
		result, err := env.QueryWorkflow(queryType)
		s.NoError(err)
		var str string
		s.NoError(result.Get(&str))
		s.Equal("query-result", str)
	}, time.Second)

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	captured := dc.getCapturedContexts()
	foundWfCtx := false
	for _, ctx := range captured {
		if _, ok := ctx.(converter.WorkflowSerializationContext); ok {
			foundWfCtx = true
			break
		}
	}
	s.True(foundWfCtx, "should have captured WorkflowSerializationContext for query")
}

func (s *SerializationContextTestSuite) TestQueryRoundTrip_SigningCodec() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	env.SetDataConverter(codecDC)

	workflowFn := func(ctx Context) error {
		err := SetQueryHandler(ctx, queryType, func() (string, error) {
			return "query-result", nil
		})
		if err != nil {
			return err
		}
		_ = Sleep(ctx, time.Minute)
		return nil
	}

	env.RegisterDelayedCallback(func() {
		result, err := env.QueryWorkflow(queryType)
		s.NoError(err)
		var str string
		s.NoError(result.Get(&str))
		s.Equal("query-result", str)
	}, time.Second)

	env.ExecuteWorkflow(workflowFn)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *SerializationContextTestSuite) TestUpdateResult_CapturingDC() {
	env := s.NewTestWorkflowEnvironment()
	dc := newSerCtxCapturingDataConverter()
	env.SetDataConverter(dc)

	updateName := "test-update"
	workflow := func(ctx Context) error {
		err := SetUpdateHandler(ctx, updateName, func(ctx Context, input string) (string, error) {
			return "updated:" + input, nil
		}, UpdateHandlerOptions{})
		if err != nil {
			return err
		}
		_ = Sleep(ctx, time.Minute)
		return nil
	}

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(updateName, "update-id", &TestUpdateCallback{
			OnAccept:   func() {},
			OnReject:   func(err error) { s.Fail("update rejected", err) },
			OnComplete: func(result interface{}, err error) { s.NoError(err) },
		}, "hello")
	}, time.Second)

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())

	captured := dc.getCapturedContexts()
	foundWfCtx := false
	for _, ctx := range captured {
		if _, ok := ctx.(converter.WorkflowSerializationContext); ok {
			foundWfCtx = true
			break
		}
	}
	s.True(foundWfCtx, "should have captured WorkflowSerializationContext for update")
}

func (s *SerializationContextTestSuite) TestUpdateRoundTrip_SigningCodec() {
	env := s.NewTestWorkflowEnvironment()
	codecDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
	env.SetDataConverter(codecDC)

	updateName := "test-update"
	workflow := func(ctx Context) error {
		err := SetUpdateHandler(ctx, updateName, func(ctx Context, input string) (string, error) {
			return "updated:" + input, nil
		}, UpdateHandlerOptions{})
		if err != nil {
			return err
		}
		_ = Sleep(ctx, time.Minute)
		return nil
	}

	var updateResult interface{}
	var updateErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(updateName, "update-id", &TestUpdateCallback{
			OnAccept:   func() {},
			OnReject:   func(err error) { updateErr = err },
			OnComplete: func(result interface{}, err error) { updateResult = result; updateErr = err },
		}, "hello")
	}, time.Second)

	env.ExecuteWorkflow(workflow)
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
	s.NoError(updateErr)
	s.NotNil(updateResult)
}
