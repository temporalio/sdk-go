package opentelemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry-v2"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor/tracing"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func nopActivity(context.Context) error { return nil }

func spanKindWorkflow(ctx workflow.Context) error {
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second})
	return workflow.ExecuteActivity(actx, nopActivity).Get(actx, nil)
}

type interceptorTestSuite struct{ otelTestSuite }

func TestInterceptorTestSuite(t *testing.T) { suite.Run(t, new(interceptorTestSuite)) }

func (s *interceptorTestSuite) TestCodecHeaders() {
	for _, enabled := range []bool{false, true} {
		s.Run(map[bool]string{false: "legacy", true: "enabled"}[enabled], func() {
			recorder := s.newSpanRecorder()
			plugin, err := temporalotel.NewPlugin(temporalotel.PluginOptions{
				TracerOptions: tracing.TracerOptions{AddTemporalSpans: true, EncodeHeaders: enabled},
			})
			s.Require().NoError(err)
			c := s.newDevServerClient(client.Options{
				Plugins: []client.Plugin{plugin},
				DataConverter: converter.NewCodecDataConverter(
					converter.GetDefaultDataConverter(),
					converter.NewZlibCodec(converter.ZlibCodecOptions{AlwaysEncode: true})),
			}, testsuite.DevServerOptions{})
			queue := uuid.NewString()
			w := worker.New(c, queue, worker.Options{})
			w.RegisterWorkflow(spanKindWorkflow)
			w.RegisterActivity(nopActivity)
			s.Require().NoError(w.Start())
			defer w.Stop()
			run, err := c.ExecuteWorkflow(context.Background(),
				client.StartWorkflowOptions{TaskQueue: queue}, spanKindWorkflow)
			s.Require().NoError(err)
			s.Require().NoError(run.Get(context.Background(), nil))
			history := c.GetWorkflowHistory(context.Background(), run.GetID(), run.GetRunID(),
				false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			event, err := history.Next()
			s.Require().NoError(err)
			payload := event.GetWorkflowExecutionStartedEventAttributes().GetHeader().GetFields()["_tracer-data"]
			s.Require().NotNil(payload)
			expected := "json/plain"
			if enabled {
				expected = "binary/zlib"
			}
			s.Equal(expected, string(payload.Metadata[converter.MetadataEncoding]))
			s.requireSpanNamed(recorder.Ended(), "RunActivity:nopActivity")
		})
	}
}
