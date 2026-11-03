package internal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/converter"
)

type dataConverterKeysPropagator struct {
	keysPropagator
	dc               converter.DataConverter
	useDataConverter bool
}

var _ DataConverterContextPropagator = (*dataConverterKeysPropagator)(nil)

func (p *dataConverterKeysPropagator) dataConverter() converter.DataConverter {
	if p.useDataConverter {
		return p.dc
	}
	return converter.GetDefaultDataConverter()
}

func (p *dataConverterKeysPropagator) WithDataConverter(dc converter.DataConverter) ContextPropagator {
	bound := *p
	bound.dc = dc
	return &bound
}

func (p *dataConverterKeysPropagator) Inject(ctx context.Context, writer HeaderWriter) error {
	for _, key := range p.keys {
		payload, err := p.dataConverter().ToPayload(ctx.Value(contextKey(key)))
		if err != nil {
			return err
		}
		writer.Set(key, payload)
	}
	return nil
}

func (p *dataConverterKeysPropagator) InjectFromWorkflow(ctx Context, writer HeaderWriter) error {
	for _, key := range p.keys {
		payload, err := p.dataConverter().ToPayload(ctx.Value(contextKey(key)))
		if err != nil {
			return err
		}
		writer.Set(key, payload)
	}
	return nil
}

func (p *dataConverterKeysPropagator) Extract(ctx context.Context, reader HeaderReader) (context.Context, error) {
	for _, key := range p.keys {
		if payload, ok := reader.Get(key); ok {
			var value string
			if err := p.dataConverter().FromPayload(payload, &value); err != nil {
				return ctx, err
			}
			ctx = context.WithValue(ctx, contextKey(key), value)
		}
	}
	return ctx, nil
}

func (p *dataConverterKeysPropagator) ExtractToWorkflow(ctx Context, reader HeaderReader) (Context, error) {
	for _, key := range p.keys {
		if payload, ok := reader.Get(key); ok {
			var value string
			if err := p.dataConverter().FromPayload(payload, &value); err != nil {
				return ctx, err
			}
			ctx = WithValue(ctx, contextKey(key), value)
		}
	}
	return ctx, nil
}

func TestDataConverterContextPropagatorWorkflowEnvironment(t *testing.T) {
	for _, operation := range []string{"activity", "local-activity", "child", "continue-as-new"} {
		t.Run(operation, func(t *testing.T) {
			var suite WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{})
			propagator := &dataConverterKeysPropagator{keysPropagator: keysPropagator{keys: []string{"tenant"}}, useDataConverter: true}
			env.SetDataConverter(dc)
			env.SetContextPropagators([]ContextPropagator{propagator})
			env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "parent"})
			initialDC := converter.WithDataConverterSerializationContext(dc, converter.WorkflowSerializationContext{WorkflowID: "parent"})
			initial, err := initialDC.ToPayload("initial")
			require.NoError(t, err)
			env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"tenant": initial}})

			activity := func(ctx context.Context) (string, error) {
				value, _ := ctx.Value(contextKey("tenant")).(string)
				return value, nil
			}
			child := func(ctx Context) (string, error) {
				value, _ := ctx.Value(contextKey("tenant")).(string)
				return value, nil
			}
			env.RegisterActivity(activity)
			env.RegisterWorkflow(child)
			env.ExecuteWorkflow(func(ctx Context) (string, error) {
				require.Equal(t, "initial", ctx.Value(contextKey("tenant")))
				ctx = WithValue(ctx, contextKey("tenant"), "outbound")
				var result string
				var err error
				switch operation {
				case "activity":
					ctx = WithActivityOptions(ctx, ActivityOptions{StartToCloseTimeout: time.Minute})
					err = ExecuteActivity(ctx, activity).Get(ctx, &result)
				case "local-activity":
					ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{StartToCloseTimeout: time.Minute})
					err = ExecuteLocalActivity(ctx, activity).Get(ctx, &result)
				case "child":
					ctx = WithChildWorkflowOptions(ctx, ChildWorkflowOptions{WorkflowID: "child"})
					err = ExecuteChildWorkflow(ctx, child).Get(ctx, &result)
				case "continue-as-new":
					return "", NewContinueAsNewError(ctx, child)
				}
				return result, err
			})
			require.True(t, env.IsWorkflowCompleted())
			if operation == "continue-as-new" {
				var continueErr *ContinueAsNewError
				require.ErrorAs(t, env.GetWorkflowError(), &continueErr)
				payload := continueErr.Header.Fields["tenant"]
				require.NotNil(t, payload)
				require.Equal(t, "parent", string(payload.Metadata["ctx-signature"]))
				var value string
				require.NoError(t, initialDC.FromPayload(payload, &value))
				require.Equal(t, "outbound", value)
			} else {
				require.NoError(t, env.GetWorkflowError())
				var result string
				require.NoError(t, env.GetWorkflowResult(&result))
				require.Equal(t, "outbound", result)
			}
			require.Nil(t, propagator.dc, "binding must not mutate the shared propagator")
		})
	}
}

func TestDataConverterContextPropagatorGoHeaders(t *testing.T) {
	propagator := &dataConverterKeysPropagator{keysPropagator: keysPropagator{keys: []string{"tenant"}}, useDataConverter: true}
	for _, workflowID := range []string{"first", "second"} {
		t.Run(workflowID, func(t *testing.T) {
			t.Parallel()
			dc := converter.WithDataConverterSerializationContext(
				converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}),
				converter.WorkflowSerializationContext{WorkflowID: workflowID},
			)
			ctx := context.WithValue(contextWithNewHeader(t.Context()), contextKey("tenant"), workflowID)
			header, err := headerPropagated(ctx, []ContextPropagator{propagator}, dc)
			require.NoError(t, err)
			require.Equal(t, workflowID, string(header.Fields["tenant"].Metadata["ctx-signature"]))
			extracted, err := contextWithHeaderPropagated(t.Context(), header, []ContextPropagator{propagator}, dc)
			require.NoError(t, err)
			require.Equal(t, workflowID, extracted.Value(contextKey("tenant")))
			require.Nil(t, propagator.dc)
		})
	}
}

func TestDataConverterContextPropagatorLegacyHeaders(t *testing.T) {
	dc := converter.WithDataConverterSerializationContext(
		converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{}),
		converter.WorkflowSerializationContext{WorkflowID: "must-not-encode-legacy"},
	)
	propagators := []ContextPropagator{NewKeysPropagator([]string{"tenant"})}
	ctx := context.WithValue(contextWithNewHeader(t.Context()), contextKey("tenant"), "legacy")
	header, err := headerPropagated(ctx, propagators, dc)
	require.NoError(t, err)
	require.NotContains(t, header.Fields["tenant"].Metadata, "ctx-signature")
	extracted, err := contextWithHeaderPropagated(t.Context(), header, propagators, dc)
	require.NoError(t, err)
	require.Equal(t, "legacy", extracted.Value(contextKey("tenant")))
}

func TestDataConverterContextPropagatorDisabled(t *testing.T) {
	defaultDC := converter.GetDefaultDataConverter()
	propagator := &dataConverterKeysPropagator{
		keysPropagator: keysPropagator{keys: []string{"tenant"}},
		dc:             defaultDC,
	}
	propagators := []ContextPropagator{propagator}
	dc := &propagationFailureDataConverter{DataConverter: defaultDC, err: errors.New("must not be injected")}
	ctx := context.WithValue(contextWithNewHeader(t.Context()), contextKey("tenant"), "plaintext")
	header, err := headerPropagated(ctx, propagators, dc)
	require.NoError(t, err)
	require.Equal(t, "json/plain", string(header.Fields["tenant"].Metadata[converter.MetadataEncoding]))
	extracted, err := contextWithHeaderPropagated(t.Context(), header, propagators, dc)
	require.NoError(t, err)
	require.Equal(t, "plaintext", extracted.Value(contextKey("tenant")))

	workflowCtx := workflowContextWithNewHeader(WithDataConverter(Background(), dc))
	workflowCtx = WithValue(workflowCtx, contextKey("tenant"), "plaintext")
	header, err = workflowHeaderPropagated(workflowCtx, propagators, dc)
	require.NoError(t, err)
	require.Equal(t, "json/plain", string(header.Fields["tenant"].Metadata[converter.MetadataEncoding]))
	extractedWorkflow, err := workflowContextWithHeaderPropagated(workflowCtx, header, propagators)
	require.NoError(t, err)
	require.Equal(t, "plaintext", extractedWorkflow.Value(contextKey("tenant")))
	require.Same(t, defaultDC, propagator.dc)
}

type propagationFailureDataConverter struct {
	converter.DataConverter
	err error
}

func (dc *propagationFailureDataConverter) ToPayload(any) (*commonpb.Payload, error) {
	return nil, dc.err
}

func (dc *propagationFailureDataConverter) FromPayload(*commonpb.Payload, any) error {
	return dc.err
}

func TestDataConverterContextPropagatorConverterFailures(t *testing.T) {
	cause := errors.New("header conversion failed")
	dc := &propagationFailureDataConverter{DataConverter: converter.GetDefaultDataConverter(), err: cause}
	propagators := []ContextPropagator{&dataConverterKeysPropagator{keysPropagator: keysPropagator{keys: []string{"tenant"}}, useDataConverter: true}}
	header, err := headerPropagated(contextWithNewHeader(t.Context()), propagators, dc)
	require.Nil(t, header)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "failed propagating header")
	ctx, err := contextWithHeaderPropagated(t.Context(),
		&commonpb.Header{Fields: map[string]*commonpb.Payload{"tenant": {}}}, propagators, dc)
	require.Nil(t, ctx)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "failed propagating header")

	workflowCtx := workflowContextWithNewHeader(WithDataConverter(Background(), dc))
	header, err = workflowHeaderPropagated(workflowCtx, propagators, dc)
	require.Nil(t, header)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "failed propagating header")
	extracted, err := workflowContextWithHeaderPropagated(workflowCtx,
		&commonpb.Header{Fields: map[string]*commonpb.Payload{"tenant": {}}}, propagators)
	require.Nil(t, extracted)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "failed propagating header")
}

type propagationContextDataConverter struct {
	converter.DataConverter
	t *testing.T
}

func (dc *propagationContextDataConverter) WithContext(ctx context.Context) converter.DataConverter {
	require.Equal(dc.t, "bound", ctx.Value(contextKey("binding")))
	return converter.NewCodecDataConverter(dc.DataConverter, &serCtxSigningCodec{signature: "bound"})
}

func (dc *propagationContextDataConverter) WithWorkflowContext(ctx Context) converter.DataConverter {
	require.Equal(dc.t, "bound", ctx.Value(contextKey("binding")))
	return converter.NewCodecDataConverter(dc.DataConverter, &serCtxSigningCodec{signature: "bound"})
}

func TestDataConverterContextPropagatorInboundContextBinding(t *testing.T) {
	dc := &propagationContextDataConverter{DataConverter: converter.GetDefaultDataConverter(), t: t}
	signedDC := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), &serCtxSigningCodec{signature: "bound"})
	payload, err := signedDC.ToPayload("tenant-value")
	require.NoError(t, err)
	binding, err := converter.GetDefaultDataConverter().ToPayload("bound")
	require.NoError(t, err)
	header := &commonpb.Header{Fields: map[string]*commonpb.Payload{"binding": binding, "tenant": payload}}
	propagators := []ContextPropagator{
		NewKeysPropagator([]string{"binding"}),
		&dataConverterKeysPropagator{keysPropagator: keysPropagator{keys: []string{"tenant"}}, useDataConverter: true},
	}
	extracted, err := contextWithHeaderPropagated(t.Context(), header, propagators, dc)
	require.NoError(t, err)
	require.Equal(t, "tenant-value", extracted.Value(contextKey("tenant")))
	workflowCtx, err := workflowContextWithHeaderPropagated(WithDataConverter(Background(), dc), header, propagators)
	require.NoError(t, err)
	require.Equal(t, "tenant-value", workflowCtx.Value(contextKey("tenant")))
}
