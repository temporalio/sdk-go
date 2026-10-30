package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/proto"

	"go.temporal.io/sdk/converter"
	commonconverter "go.temporal.io/sdk/internal/common/converter"
)

type headerCodecFunc func([]*commonpb.Payload) ([]*commonpb.Payload, error)

type codecKeysPropagator struct{ ContextPropagator }

func (codecKeysPropagator) EncodeHeaders() bool { return true }

func (f headerCodecFunc) Encode(p []*commonpb.Payload) ([]*commonpb.Payload, error) { return f(p) }
func (f headerCodecFunc) Decode(p []*commonpb.Payload) ([]*commonpb.Payload, error) { return f(p) }

type forwardingHeaderConverter struct{ converter.DataConverter }

func (c forwardingHeaderConverter) Codec() converter.PayloadCodec {
	return c.DataConverter.(converter.PayloadCodecProvider).Codec()
}

type nilContextHeaderCodec struct {
	converter.DataConverter
	converter.PayloadCodec
}

func (c nilContextHeaderCodec) Codec() converter.PayloadCodec { return c }
func (nilContextHeaderCodec) WithSerializationContext(converter.SerializationContext) converter.PayloadCodec {
	return nil
}

func TestHeaderCodecContextBinding(t *testing.T) {
	defaultDC := converter.GetDefaultDataConverter()
	dropping := nilContextHeaderCodec{
		DataConverter: defaultDC, PayloadCodec: converter.NewZlibCodec(converter.ZlibCodecOptions{}),
	}
	payload, err := defaultDC.ToPayload("header")
	require.NoError(t, err)
	for name, dc := range map[string]converter.DataConverter{
		"direct provider":      dropping,
		"nil configured stage": converter.NewCodecDataConverter(defaultDC, nil),
		"built-in chain": converter.NewCodecDataConverter(defaultDC, dropping,
			converter.NewZlibCodec(converter.ZlibCodecOptions{})),
	} {
		t.Run(name, func(t *testing.T) {
			client := NewServiceClient(nil, nil, ClientOptions{DataConverter: dc})
			ctx := contextWithNewHeader(t.Context())
			Header(ctx)["key"] = payload
			_, err := headerPropagated(ctx, nil, client.workflowHeaderCodec("target"))
			require.NoError(t, err)
			EncodeHeader(ctx, "key")
			_, err = headerPropagated(ctx, nil, client.workflowHeaderCodec("target"))
			require.ErrorContains(t, err, "nil")
			_, err = codecHeader(&commonpb.Header{Fields: Header(ctx)}, client.workflowHeaderCodec("target"), false)
			require.ErrorContains(t, err, "nil")
			require.Same(t, payload, Header(ctx)["key"])
		})
	}
}

type checkingContextHeaderCodec struct {
	converter.PayloadCodec
	bind func(converter.SerializationContext)
}

func (c checkingContextHeaderCodec) WithSerializationContext(sc converter.SerializationContext) converter.PayloadCodec {
	c.bind(sc)
	return c.PayloadCodec
}

func TestHeaderCodecWithoutDeadlockDetection(t *testing.T) {
	var running Context
	calls, bindings := 0, 0
	var stages []string
	checkPaused := func() {
		if running != nil {
			d := getDeadlockDetector(running)
			assert.NotNil(t, d)
			if d != nil {
				assert.True(t, d.paused)
			}
		}
	}
	dc := converter.NewCodecDataConverter(
		converter.GetDefaultDataConverter(), checkingContextHeaderCodec{
			PayloadCodec: headerCodecFunc(func(p []*commonpb.Payload) ([]*commonpb.Payload, error) {
				checkPaused()
				calls++
				stages = append(stages, "inner")
				return p, nil
			}),
			bind: func(converter.SerializationContext) {
				checkPaused()
				bindings++
			},
		})
	wrapped := commonconverter.MakeTransferAware(DataConverterWithoutDeadlockDetection(dc))
	outerBindings := 0
	checkOuter := func() {
		if running != nil {
			assert.False(t, getDeadlockDetector(running).paused)
		}
	}
	composed := converter.NewCodecDataConverter(wrapped, checkingContextHeaderCodec{
		PayloadCodec: headerCodecFunc(func(p []*commonpb.Payload) ([]*commonpb.Payload, error) {
			checkOuter()
			stages = append(stages, "outer")
			return p, nil
		}),
		bind: func(converter.SerializationContext) {
			checkOuter()
			outerBindings++
		},
	})
	stored := headerCodecFromDataConverter(composed)
	header := &commonpb.Header{Fields: map[string]*commonpb.Payload{"key": encodeString(t, "header")}}
	process := func(codec converter.PayloadCodec) {
		encoded, err := codecHeader(header, codec, true)
		assert.NoError(t, err)
		decoded, err := codecHeader(encoded, codec, false)
		assert.NoError(t, err)
		assert.True(t, proto.Equal(header, decoded))
	}
	// Client and inbound processing can use the same stored codec without a coroutine.
	process(bindHeaderCodec(stored, converter.WorkflowSerializationContext{WorkflowID: "client"}))
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) error {
		running = ctx
		defer func() { running = nil }()
		getWorkflowEnvOptions(ctx).headerCodec = stored
		for _, sc := range []converter.SerializationContext{
			converter.ActivitySerializationContext{ActivityType: "activity"},
			converter.WorkflowSerializationContext{WorkflowID: "child"},
		} {
			process(workflowHeaderCodec(ctx, sc))
			assert.False(t, getDeadlockDetector(ctx).paused)
		}
		return nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 3, bindings)
	require.Equal(t, 3, outerBindings)
	require.Equal(t, 6, calls)
	require.Equal(t, []string{
		"inner", "outer", "outer", "inner",
		"inner", "outer", "outer", "inner",
		"inner", "outer", "outer", "inner",
	}, stages)
	require.Nil(t, headerCodecFromDataConverter(DataConverterWithoutDeadlockDetection(converter.GetDefaultDataConverter())))
}

func TestHeaderCodecFailure(t *testing.T) {
	payload, err := converter.GetDefaultDataConverter().ToPayload("header")
	require.NoError(t, err)
	original := append([]byte(nil), payload.Data...)
	header := &commonpb.Header{Fields: map[string]*commonpb.Payload{"key": payload}}
	failure := errors.New("codec failure")
	codec := headerCodecFunc(func(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
		payloads[0].Data[0] = 0
		return nil, failure
	})
	_, err = codecHeader(header, codec, true)
	require.ErrorIs(t, err, failure)
	require.Equal(t, original, payload.Data)
	_, err = codecHeader(header, codec, false)
	require.ErrorIs(t, err, failure)
	require.Equal(t, original, payload.Data)
	_, err = codecHeader(header, headerCodecFunc(func(p []*commonpb.Payload) ([]*commonpb.Payload, error) {
		return append(p, p...), nil
	}), true)
	require.ErrorContains(t, err, `header "key" codec returned 2 payloads, expected 1`)
}

func TestClientHeaderCodec(t *testing.T) {
	// A byte-only converter cannot value-convert the standard JSON tracing
	// payload below. Header processing must use only its codec stages.
	custom := converter.NewCompositeDataConverter(converter.NewByteSlicePayloadConverter())
	dc := converter.NewCodecDataConverter(custom, &serCtxSigningCodec{},
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	payload, err := converter.GetDefaultDataConverter().ToPayload("trace")
	require.NoError(t, err)
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "enabled"}[enabled], func(t *testing.T) {
			options := ClientOptions{DataConverter: forwardingHeaderConverter{dc}}
			client := NewServiceClient(nil, nil, options)
			interceptor := &workflowClientInterceptor{client: client}
			ctx := contextWithNewHeader(t.Context())
			Header(ctx)["_tracer-data"] = payload
			if enabled {
				EncodeHeader(ctx, "_tracer-data")
			}
			start, err := interceptor.createStartWorkflowRequest(ctx, &ClientExecuteWorkflowInput{
				WorkflowType: "workflow", Options: &StartWorkflowOptions{ID: "target", TaskQueue: "queue"},
			})
			require.NoError(t, err)
			update, err := interceptor.createUpdateWorkflowRequest(ctx, &ClientUpdateWorkflowInput{WorkflowID: "target"},
				client.workflowHeaderCodec("target"))
			require.NoError(t, err)
			action, err := convertToPBScheduleAction(ctx, client, &ScheduleWorkflowAction{
				ID: "target", Workflow: "workflow", TaskQueue: "queue",
			})
			require.NoError(t, err)
			for _, header := range []*commonpb.Header{start.Header, update.Request.Input.Header, action.GetStartWorkflow().Header} {
				p := header.Fields["_tracer-data"]
				if enabled {
					require.Equal(t, "target", string(p.Metadata["ctx-signature"]))
					require.Equal(t, "binary/zlib", string(p.Metadata[converter.MetadataEncoding]))
					decoded, err := codecHeader(header, client.workflowHeaderCodec("target"), false)
					require.NoError(t, err)
					require.True(t, proto.Equal(payload, decoded.Fields["_tracer-data"]))
				} else {
					require.True(t, proto.Equal(payload, p))
				}
			}
			require.Equal(t, "json/plain", string(payload.Metadata[converter.MetadataEncoding]))
			require.Same(t, payload, Header(ctx)["_tracer-data"])
		})
	}
}

func TestHeaderCodecWorkflowTestEnvironment(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	payload, err := dc.ToPayload("context")
	require.NoError(t, err)
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetDataConverter(dc)
	env.SetContextPropagators([]ContextPropagator{codecKeysPropagator{NewKeysPropagator([]string{"encoded"})}})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"encoded": payload}})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		return ctx.Value(contextKey("encoded")).(string), nil
	})
	require.NoError(t, env.GetWorkflowError())
	var result string
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "context", result)
	require.Equal(t, "binary/zlib", string(payload.Metadata[converter.MetadataEncoding]))
}

func TestHeaderCodecProducerIsolation(t *testing.T) {
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(),
		converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true}))
	codec := headerCodecFromDataConverter(dc)
	raw, err := dc.ToPayload("already encoded")
	require.NoError(t, err)
	ctx := contextWithNewHeader(context.WithValue(
		context.WithValue(t.Context(), contextKey("legacy"), "legacy context"),
		contextKey("encoded"), "context"))
	Header(ctx)["raw"] = raw
	Header(ctx)["plain"], err = converter.GetDefaultDataConverter().ToPayload("unencoded")
	require.NoError(t, err)
	plain := Header(ctx)["plain"]
	Header(ctx)["overwritten"] = plain
	EncodeHeader(ctx, "overwritten")
	Header(ctx)["overwritten"] = raw
	Header(ctx)["reinserted"] = plain
	EncodeHeader(ctx, "reinserted")
	delete(Header(ctx), "reinserted")
	Header(ctx)["reinserted"] = plain
	props := []ContextPropagator{
		codecKeysPropagator{NewKeysPropagator([]string{"encoded"})},
		NewKeysPropagator([]string{"legacy"}),
	}
	wire, err := headerPropagated(ctx, props, codec)
	require.NoError(t, err)
	require.True(t, proto.Equal(raw, wire.Fields["raw"]))
	require.True(t, proto.Equal(plain, wire.Fields["plain"]))
	require.True(t, proto.Equal(raw, wire.Fields["overwritten"]))
	require.Equal(t, "binary/zlib", string(wire.Fields["reinserted"].Metadata[converter.MetadataEncoding]))
	require.Equal(t, "binary/zlib", string(wire.Fields["encoded"].Metadata[converter.MetadataEncoding]))
	require.NotContains(t, Header(ctx), "encoded")
	before := proto.Clone(wire)
	inbound, err := contextWithHeaderPropagated(t.Context(), wire, props, codec)
	require.NoError(t, err)
	require.Equal(t, "context", inbound.Value(contextKey("encoded")))
	require.Equal(t, "legacy context", inbound.Value(contextKey("legacy")))
	require.True(t, proto.Equal(before, wire))
	require.True(t, proto.Equal(raw, Header(inbound)["raw"]))
	decoded, err := DecodedHeader(inbound)
	require.NoError(t, err)
	require.Equal(t, "json/plain", string(decoded["raw"].Metadata[converter.MetadataEncoding]))
	require.True(t, proto.Equal(plain, decoded["plain"]))
}

func TestHeaderWriter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		initial  *commonpb.Header
		expected *commonpb.Header
		vals     map[string]*commonpb.Payload
	}{
		{
			"no values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			map[string]*commonpb.Payload{},
		},
		{
			"add values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]*commonpb.Payload{
				"key1": encodeString(t, "val1"),
				"key2": encodeString(t, "val2"),
			},
		},
		{
			"overwrite values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "unexpected"),
				},
			},
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]*commonpb.Payload{
				"key1": encodeString(t, "val1"),
				"key2": encodeString(t, "val2"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := NewHeaderWriter(test.initial)
			for key, val := range test.vals {
				writer.Set(key, val)
			}
			assert.Equal(t, test.expected, test.initial)
		})
	}
}

func encodeString(t *testing.T, s string) *commonpb.Payload {
	p, err := converter.GetDefaultDataConverter().ToPayload(s)
	assert.NoError(t, err)
	return p
}

func TestHeaderReader_ForEachKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		header  *commonpb.Header
		keys    map[string]struct{}
		isError bool
	}{
		{
			"valid values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]struct{}{"key1": {}, "key2": {}},
			false,
		},
		{
			"invalid values",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			map[string]struct{}{"key2": {}},
			true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := NewHeaderReader(test.header)
			err := reader.ForEachKey(func(key string, _ *commonpb.Payload) error {
				if _, ok := test.keys[key]; !ok {
					return assert.AnError
				}
				return nil
			})
			if test.isError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHeaderReader_Get(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		header       *commonpb.Header
		key          string
		headerExists bool
	}{
		{
			"valid key",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			"key1",
			true,
		},
		{
			"invalid key",
			&commonpb.Header{
				Fields: map[string]*commonpb.Payload{
					"key1": encodeString(t, "val1"),
					"key2": encodeString(t, "val2"),
				},
			},
			"key3",
			false,
		},
		{
			"nil fields",
			&commonpb.Header{},
			"key1",
			false,
		},
		{
			"nil headers",
			nil,
			"key1",
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := NewHeaderReader(test.header)
			_, headerExist := reader.Get(test.key)
			if test.headerExists {
				assert.True(t, headerExist)
			} else {
				assert.False(t, headerExist)
			}
		})
	}
}
