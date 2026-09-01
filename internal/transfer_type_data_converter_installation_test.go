package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/converter"
)

func requireInstalledTransferTypeDataConverter(
	t *testing.T,
	dc converter.DataConverter,
	parent converter.DataConverter,
) *transferTypeDataConverter {
	t.Helper()

	wrapped, ok := dc.(*transferTypeDataConverter)
	require.Truef(t, ok, "data converter has type %T, want *transferTypeDataConverter", dc)
	require.Same(t, parent, wrapped.parent)
	return wrapped
}

func requireTransferStringPayload(t *testing.T, payload *commonpb.Payload, want string) {
	t.Helper()

	var got string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(payload, &got))
	require.Equal(t, want, got)
}

func requireInstalledTransferBehavior(t *testing.T, dc converter.DataConverter, value string) {
	t.Helper()

	payload, err := dc.ToPayload(stringTransferValue{Value: value})
	require.NoError(t, err)
	requireTransferStringPayload(t, payload, value)
}

func TestTransferTypeDataConverterServiceClientInstallation(t *testing.T) {
	globalDefault := converter.GetDefaultDataConverter()
	_, globalDefaultIsWrapped := globalDefault.(*transferTypeDataConverter)
	require.False(t, globalDefaultIsWrapped)

	t.Run("default", func(t *testing.T) {
		options := ClientOptions{}
		client := NewServiceClient(nil, nil, options)

		require.Nil(t, options.DataConverter, "NewServiceClient mutated caller options")
		requireInstalledTransferTypeDataConverter(t, client.dataConverter, globalDefault)
		requireInstalledTransferBehavior(t, client.dataConverter, "default client")
	})

	t.Run("custom", func(t *testing.T) {
		parent := newRecordingDataConverter()
		options := ClientOptions{DataConverter: parent}
		client := NewServiceClient(nil, nil, options)

		require.Same(t, parent, options.DataConverter, "NewServiceClient mutated caller options")
		requireInstalledTransferTypeDataConverter(t, client.dataConverter, parent)
		requireInstalledTransferBehavior(t, client.dataConverter, "custom client")
		require.Equal(t, []any{"custom client"}, parent.toPayloadValues)
	})

	t.Run("plugin replaces converter before wrapping", func(t *testing.T) {
		callerParent := newRecordingDataConverter()
		pluginParent := newRecordingDataConverter()
		pluginConfigured := false
		plugin, err := NewSimplePlugin(SimplePluginOptions{
			Name:          "transfer-installation-client",
			DataConverter: pluginParent,
			ConfigureClient: func(_ context.Context, options ClientPluginConfigureClientOptions) error {
				pluginConfigured = true
				require.Same(t, pluginParent, options.ClientOptions.DataConverter)
				_, alreadyWrapped := options.ClientOptions.DataConverter.(*transferTypeDataConverter)
				require.False(t, alreadyWrapped)
				return nil
			},
		})
		require.NoError(t, err)

		options := ClientOptions{
			DataConverter: callerParent,
			Plugins:       []ClientPlugin{plugin},
		}
		clientInterface, err := NewLazyClient(options)
		require.NoError(t, err)
		t.Cleanup(clientInterface.Close)

		client, ok := clientInterface.(*WorkflowClient)
		require.Truef(t, ok, "client has type %T, want *WorkflowClient", clientInterface)
		require.True(t, pluginConfigured)
		require.Same(t, callerParent, options.DataConverter, "client plugin mutated caller options")
		requireInstalledTransferTypeDataConverter(t, client.dataConverter, pluginParent)
		requireInstalledTransferBehavior(t, client.dataConverter, "plugin client")
		require.Empty(t, callerParent.toPayloadValues)
		require.Equal(t, []any{"plugin client"}, pluginParent.toPayloadValues)
	})

	require.Same(t, globalDefault, converter.GetDefaultDataConverter())
	payload, err := globalDefault.ToPayload(stringTransferValue{Value: "global default"})
	require.NoError(t, err)
	require.NotEqual(t, []byte(`"global default"`), payload.Data, "global default unexpectedly applies transfer conversion")
}

func TestTransferTypeDataConverterWorkerDefaultsInstallation(t *testing.T) {
	globalDefault := converter.GetDefaultDataConverter()

	t.Run("ensureRequiredParams defaults and wraps", func(t *testing.T) {
		params := &workerExecutionParameters{}
		ensureRequiredParams(params)

		first := requireInstalledTransferTypeDataConverter(t, params.DataConverter, globalDefault)
		requireInstalledTransferBehavior(t, first, "default worker")

		ensureRequiredParams(params)
		require.Same(t, first, params.DataConverter)
	})

	t.Run("ensureRequiredParams preserves custom parent and is idempotent", func(t *testing.T) {
		parent := newRecordingDataConverter()
		params := &workerExecutionParameters{DataConverter: parent}
		ensureRequiredParams(params)

		first := requireInstalledTransferTypeDataConverter(t, params.DataConverter, parent)
		requireInstalledTransferBehavior(t, first, "custom worker")

		ensureRequiredParams(params)
		require.Same(t, first, params.DataConverter)
		require.Equal(t, []any{"custom worker"}, parent.toPayloadValues)
	})

	t.Run("setClientDefaults defaults and wraps", func(t *testing.T) {
		client := &WorkflowClient{}
		setClientDefaults(client)

		first := requireInstalledTransferTypeDataConverter(t, client.dataConverter, globalDefault)
		requireInstalledTransferBehavior(t, first, "default test client")

		setClientDefaults(client)
		require.Same(t, first, client.dataConverter)
	})

	t.Run("setClientDefaults preserves custom parent and is idempotent", func(t *testing.T) {
		parent := newRecordingDataConverter()
		client := &WorkflowClient{dataConverter: parent}
		setClientDefaults(client)

		first := requireInstalledTransferTypeDataConverter(t, client.dataConverter, parent)
		requireInstalledTransferBehavior(t, first, "custom test client")

		setClientDefaults(client)
		require.Same(t, first, client.dataConverter)
		require.Equal(t, []any{"custom test client"}, parent.toPayloadValues)
	})
}

func TestTransferTypeDataConverterWorkflowReplayerInstallation(t *testing.T) {
	globalDefault := converter.GetDefaultDataConverter()

	t.Run("default", func(t *testing.T) {
		options := WorkflowReplayerOptions{}
		replayer, err := NewWorkflowReplayer(options)
		require.NoError(t, err)

		require.Nil(t, options.DataConverter, "NewWorkflowReplayer mutated caller options")
		requireInstalledTransferTypeDataConverter(t, replayer.dataConverter, globalDefault)
		requireInstalledTransferBehavior(t, replayer.dataConverter, "default replayer")
	})

	t.Run("custom", func(t *testing.T) {
		parent := newRecordingDataConverter()
		options := WorkflowReplayerOptions{DataConverter: parent}
		replayer, err := NewWorkflowReplayer(options)
		require.NoError(t, err)

		require.Same(t, parent, options.DataConverter, "NewWorkflowReplayer mutated caller options")
		requireInstalledTransferTypeDataConverter(t, replayer.dataConverter, parent)
		requireInstalledTransferBehavior(t, replayer.dataConverter, "custom replayer")
		require.Equal(t, []any{"custom replayer"}, parent.toPayloadValues)
	})

	t.Run("plugin replaces converter before wrapping", func(t *testing.T) {
		callerParent := newRecordingDataConverter()
		pluginParent := newRecordingDataConverter()
		pluginConfigured := false
		plugin, err := NewSimplePlugin(SimplePluginOptions{
			Name:          "transfer-installation-replayer",
			DataConverter: pluginParent,
			ConfigureWorkflowReplayer: func(
				_ context.Context,
				options WorkerPluginConfigureWorkflowReplayerOptions,
			) error {
				pluginConfigured = true
				require.Same(t, pluginParent, options.WorkflowReplayerOptions.DataConverter)
				_, alreadyWrapped := options.WorkflowReplayerOptions.DataConverter.(*transferTypeDataConverter)
				require.False(t, alreadyWrapped)
				return nil
			},
		})
		require.NoError(t, err)

		options := WorkflowReplayerOptions{
			DataConverter: callerParent,
			Plugins:       []WorkerPlugin{plugin},
		}
		replayer, err := NewWorkflowReplayer(options)
		require.NoError(t, err)

		require.True(t, pluginConfigured)
		require.Same(t, callerParent, options.DataConverter, "replayer plugin mutated caller options")
		requireInstalledTransferTypeDataConverter(t, replayer.dataConverter, pluginParent)
		requireInstalledTransferBehavior(t, replayer.dataConverter, "plugin replayer")
		require.Empty(t, callerParent.toPayloadValues)
		require.Equal(t, []any{"plugin replayer"}, pluginParent.toPayloadValues)
	})
}

func TestTransferTypeDataConverterWorkflowOverrideInstallation(t *testing.T) {
	parent := newRecordingDataConverter()
	ctx := WithDataConverter(Background(), parent)
	options := getWorkflowEnvOptions(ctx)

	require.NotNil(t, options)
	require.Same(t, options.DataConverter, options.RootDataConverter)
	requireInstalledTransferTypeDataConverter(t, options.DataConverter, parent)
	requireInstalledTransferBehavior(t, options.DataConverter, "workflow override")
	require.Equal(t, []any{"workflow override"}, parent.toPayloadValues)

	require.PanicsWithValue(t, "data converter is nil for WithDataConverter", func() {
		WithDataConverter(Background(), nil)
	})
}

func TestTransferTypeDataConverterTestEnvironmentInstallation(t *testing.T) {
	globalDefault := converter.GetDefaultDataConverter()
	var suite WorkflowTestSuite

	t.Run("default workflow environment", func(t *testing.T) {
		env := suite.NewTestWorkflowEnvironment()
		installed := requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, globalDefault)

		require.Nil(t, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())
		requireInstalledTransferBehavior(t, installed, "default workflow environment")
	})

	t.Run("default activity environment", func(t *testing.T) {
		env := suite.NewTestActivityEnvironment()
		installed := requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, globalDefault)

		require.Nil(t, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())
		requireInstalledTransferBehavior(t, installed, "default activity environment")
	})

	t.Run("custom workflow environment preserves and replaces root", func(t *testing.T) {
		parent := newRecordingDataConverter()
		env := suite.NewTestWorkflowEnvironment()
		env.SetDataConverter(parent)
		installed := requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, parent)

		require.Nil(t, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())

		want := stringTransferValue{Value: "custom workflow environment"}
		env.ExecuteWorkflow(
			func(_ Context, value stringTransferValue) (stringTransferValue, error) { return value, nil },
			want,
		)
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var got stringTransferValue
		require.NoError(t, env.GetWorkflowResult(&got))
		require.Equal(t, want, got)

		require.Same(t, installed, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())
		requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, parent)
		require.NotSame(t, env.impl.rootDataConverter, env.impl.dataConverter)
		require.Equal(t, []any{want.Value}, parent.toPayloadsValues)

		replacement := newRecordingDataConverter()
		env.SetDataConverter(replacement)
		require.Same(t, env.impl.dataConverter, env.impl.rootDataConverter)
		requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, replacement)
	})

	t.Run("custom activity environment", func(t *testing.T) {
		parent := newRecordingDataConverter()
		env := suite.NewTestActivityEnvironment()
		env.SetDataConverter(parent)
		installed := requireInstalledTransferTypeDataConverter(t, env.impl.dataConverter, parent)

		require.Nil(t, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())

		activity := func(_ context.Context, value stringTransferValue) (stringTransferValue, error) { return value, nil }
		env.RegisterActivity(activity)
		want := stringTransferValue{Value: "custom activity environment"}
		result, err := env.ExecuteActivity(activity, want)
		require.NoError(t, err)
		var got stringTransferValue
		require.NoError(t, result.Get(&got))
		require.Equal(t, want, got)
		require.Nil(t, env.impl.rootDataConverter)
		require.Same(t, installed, env.impl.GetRootDataConverter())
		require.Equal(t, []any{want.Value}, parent.toPayloadsValues)
	})
}

func TestTransferTypeDataConverterFailureConverterInstallation(t *testing.T) {
	globalDefault := converter.GetDefaultDataConverter()
	converterTests := []struct {
		name   string
		parent converter.DataConverter
	}{
		{name: "default"},
		{name: "custom", parent: newRecordingDataConverter()},
	}

	detailTests := []struct {
		name       string
		newError   func(stringTransferValue) error
		payloads   func(*failurepb.Failure) *commonpb.Payloads
		getDetails func(*testing.T, error) stringTransferValue
	}{
		{
			name: "application error details",
			newError: func(value stringTransferValue) error {
				return NewApplicationError("message", "type", false, nil, value)
			},
			payloads: func(failure *failurepb.Failure) *commonpb.Payloads {
				return failure.GetApplicationFailureInfo().GetDetails()
			},
			getDetails: func(t *testing.T, err error) stringTransferValue {
				var applicationErr *ApplicationError
				require.ErrorAs(t, err, &applicationErr)
				var got stringTransferValue
				require.NoError(t, applicationErr.Details(&got))
				return got
			},
		},
		{
			name: "cancellation details",
			newError: func(value stringTransferValue) error {
				return NewCanceledError(value)
			},
			payloads: func(failure *failurepb.Failure) *commonpb.Payloads {
				return failure.GetCanceledFailureInfo().GetDetails()
			},
			getDetails: func(t *testing.T, err error) stringTransferValue {
				var canceledErr *CanceledError
				require.ErrorAs(t, err, &canceledErr)
				var got stringTransferValue
				require.NoError(t, canceledErr.Details(&got))
				return got
			},
		},
		{
			name: "heartbeat timeout details",
			newError: func(value stringTransferValue) error {
				return NewTimeoutError("timeout", enumspb.TIMEOUT_TYPE_HEARTBEAT, nil, value)
			},
			payloads: func(failure *failurepb.Failure) *commonpb.Payloads {
				return failure.GetTimeoutFailureInfo().GetLastHeartbeatDetails()
			},
			getDetails: func(t *testing.T, err error) stringTransferValue {
				var timeoutErr *TimeoutError
				require.ErrorAs(t, err, &timeoutErr)
				var got stringTransferValue
				require.NoError(t, timeoutErr.LastHeartbeatDetails(&got))
				return got
			},
		},
	}

	for _, converterTest := range converterTests {
		t.Run(converterTest.name, func(t *testing.T) {
			failureConverter := NewDefaultFailureConverter(DefaultFailureConverterOptions{
				DataConverter: converterTest.parent,
			})
			expectedParent := converterTest.parent
			if expectedParent == nil {
				expectedParent = globalDefault
			}
			requireInstalledTransferTypeDataConverter(t, failureConverter.dataConverter, expectedParent)

			for _, detailTest := range detailTests {
				t.Run(detailTest.name, func(t *testing.T) {
					want := stringTransferValue{Value: converterTest.name + " " + detailTest.name}
					failure := failureConverter.ErrorToFailure(detailTest.newError(want))
					payloads := detailTest.payloads(failure)
					require.Len(t, payloads.GetPayloads(), 1)
					requireTransferStringPayload(t, payloads.GetPayloads()[0], want.Value)

					got := detailTest.getDetails(t, failureConverter.FailureToError(failure))
					require.Equal(t, want, got)
				})
			}
		})
	}
}

func TestTransferTypeDataConverterOneShotSerializationContextOwnership(t *testing.T) {
	parent := newSerCtxOneShotDataConverter()
	client := NewServiceClient(nil, nil, ClientOptions{DataConverter: parent})
	requireInstalledTransferTypeDataConverter(t, client.dataConverter, parent)

	serializationContext := converter.WorkflowSerializationContext{
		Namespace:  "namespace",
		WorkflowID: "workflow-id",
	}
	contextual := converter.WithDataConverterSerializationContext(client.dataConverter, serializationContext)
	wrapped, ok := contextual.(*transferTypeDataConverter)
	require.Truef(t, ok, "contextual data converter has type %T, want *transferTypeDataConverter", contextual)
	_, parentIsBound := wrapped.parent.(*serCtxBoundDataConverter)
	require.Truef(t, parentIsBound, "wrapper parent has type %T, want *serCtxBoundDataConverter", wrapped.parent)

	payloads, err := contextual.ToPayloads(stringTransferValue{Value: "one-shot context"})
	require.NoError(t, err)
	require.Len(t, payloads.GetPayloads(), 1)
	requireTransferStringPayload(t, payloads.GetPayloads()[0], "one-shot context")
	require.Equal(t, []converter.SerializationContext{serializationContext}, parent.getEncodeContexts())
}

func TestTransferTypeDataConverterWithoutDeadlockDetectionComposition(t *testing.T) {
	parent := newRecordingDataConverter()
	withoutDeadlock := DataConverterWithoutDeadlockDetection(parent)
	ctx := WithDataConverter(Background(), withoutDeadlock)
	options := getWorkflowEnvOptions(ctx)

	require.Same(t, options.DataConverter, options.RootDataConverter)
	wrapped := requireInstalledTransferTypeDataConverter(t, options.DataConverter, withoutDeadlock)
	deadlockWrapper, ok := wrapped.parent.(*dataConverterWithoutDeadlock)
	require.Truef(t, ok, "wrapper parent has type %T, want *dataConverterWithoutDeadlock", wrapped.parent)
	require.Same(t, parent, deadlockWrapper.underlying)

	payload, err := options.DataConverter.ToPayload(stringTransferValue{Value: "deadlock composition"})
	require.NoError(t, err)
	requireTransferStringPayload(t, payload, "deadlock composition")
	require.Equal(t, []any{"deadlock composition"}, parent.toPayloadValues)

	var got stringTransferValue
	require.NoError(t, options.DataConverter.FromPayload(payload, &got))
	require.Equal(t, stringTransferValue{Value: "deadlock composition"}, got)
}
