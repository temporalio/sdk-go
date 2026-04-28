package internal

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/api/proxy"
	querypb "go.temporal.io/api/query/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/protobuf/proto"
)

func TestPayloadLimitOptionsToLimits(t *testing.T) {
	t.Run("default value when zero", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{})
		require.NoError(t, err)
		require.Equal(t, int64(512*1024), limits.payloadSize)
		require.Equal(t, int64(2*1024), limits.memoSize)
	})

	t.Run("custom value", func(t *testing.T) {
		limits, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: 1024, MemoSizeWarning: 2048})
		require.NoError(t, err)
		require.Equal(t, int64(1024), limits.payloadSize)
		require.Equal(t, int64(2048), limits.memoSize)
	})

	t.Run("negative value returns error", func(t *testing.T) {
		_, err := payloadLimitOptionsToLimits(PayloadLimitOptions{PayloadSizeWarning: -1})
		require.Error(t, err)
	})

	t.Run("negative memo value returns error", func(t *testing.T) {
		_, err := payloadLimitOptionsToLimits(PayloadLimitOptions{MemoSizeWarning: -1})
		require.Error(t, err)
	})
}

func makeTestPayload(size int) *commonpb.Payload {
	return &commonpb.Payload{
		Data: make([]byte, size),
	}
}

func TestPayloadLimitsVisitorWarning(t *testing.T) {
	t.Run("no warning when under limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 1024}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100)})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.Empty(t, logger.Lines())
	})

	t.Run("warning when over limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 100}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.NoError(t, err)
		require.Len(t, result, 1)
		require.True(t, slices.ContainsFunc(logger.Lines(), func(line string) bool {
			return strings.Contains(line, "WARN  [TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
		}))
	})

	t.Run("no warning at exactly the limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		// Create a payload and measure its actual proto size to set limit exactly
		p := makeTestPayload(100)
		payloads := []*commonpb.Payload{p}
		exactSize := int64((&commonpb.Payloads{Payloads: payloads}).Size())
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: exactSize}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, payloads)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("nil logger does not panic", func(t *testing.T) {
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, nil)
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.NoError(t, err)
		require.Len(t, result, 1)
	})

	t.Run("zero warning limit disables warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 0}, logger)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(10000)})
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})
}

func TestPayloadLimitsVisitorError(t *testing.T) {
	t.Run("error when over error limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 100})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "error limit")
		require.Greater(t, pse.size, int64(0))
		require.Equal(t, int64(100), pse.limit)
	})

	t.Run("no error when under error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100)})
		require.NoError(t, err)
	})

	t.Run("no error at exactly the error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		p := makeTestPayload(100)
		payloads := []*commonpb.Payload{p}
		exactSize := int64((&commonpb.Payloads{Payloads: payloads}).Size())
		setErrorLimits(&payloadLimits{payloadSize: exactSize})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, payloads)
		require.NoError(t, err)
	})

	t.Run("error limits nil means no error check", func(t *testing.T) {
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100000)})
		require.NoError(t, err)
	})

	t.Run("zero error limit means no error check", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 0})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(100000)})
		require.NoError(t, err)
	})

	t.Run("changed error limit allows larger payloads", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)

		setErrorLimits(&payloadLimits{payloadSize: 1000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(2000)})
		require.Error(t, err)

		setErrorLimits(&payloadLimits{payloadSize: 100000})
		_, err = visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(2000)})
		require.NoError(t, err)
	})
}

func TestPayloadLimitsVisitorAggregation(t *testing.T) {
	t.Run("sums multiple payloads", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		// Each payload is small individually, but sum exceeds limit
		setErrorLimits(&payloadLimits{payloadSize: 100})
		ctx := &proxy.VisitPayloadsContext{}
		payloads := []*commonpb.Payload{
			makeTestPayload(30),
			makeTestPayload(30),
			makeTestPayload(30),
			makeTestPayload(30),
		}
		_, err := visitor.Visit(ctx, payloads)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("nil payloads in slice are skipped", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10000})
		ctx := &proxy.VisitPayloadsContext{}
		_, err := visitor.Visit(ctx, []*commonpb.Payload{nil, makeTestPayload(10), nil})
		require.NoError(t, err)
	})

	t.Run("empty slice", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		ctx := &proxy.VisitPayloadsContext{}
		result, err := visitor.Visit(ctx, []*commonpb.Payload{})
		require.NoError(t, err)
		require.Empty(t, result)
	})
}

func TestPayloadLimitsVisitorErrorBeforeWarning(t *testing.T) {
	// When both error and warning limits are exceeded, error takes priority
	logger := ilog.NewMemoryLogger()
	visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 50}, logger)
	setErrorLimits(&payloadLimits{payloadSize: 100})
	ctx := &proxy.VisitPayloadsContext{}
	_, err := visitor.Visit(ctx, []*commonpb.Payload{makeTestPayload(200)})
	require.Error(t, err)
	// Warning should not be logged since error short-circuits
	require.Empty(t, logger.Lines())
}

func hasWarningLine(logger *ilog.MemoryLogger) bool {
	return slices.ContainsFunc(logger.Lines(), func(line string) bool {
		return strings.Contains(line, "WARN  [TMPRL1103] Attempted to upload payloads with size that exceeded the warning limit.")
	})
}

func TestPayloadLimitsVisitorSpecializations(t *testing.T) {
	t.Run("RecordMarkerCommandAttributes error when Details exceed error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &commandpb.RecordMarkerCommandAttributes{
			Details: map[string]*commonpb.Payloads{
				"k": {Payloads: []*commonpb.Payload{makeTestPayload(200)}},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("RecordMarkerCommandAttributes warning when Details exceed warning limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		msg := &commandpb.RecordMarkerCommandAttributes{
			Details: map[string]*commonpb.Payloads{
				"k": {Payloads: []*commonpb.Payload{makeTestPayload(200)}},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasWarningLine(logger))
	})

	t.Run("RecordMarkerCommandAttributes child payloads no error and warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &commandpb.RecordMarkerCommandAttributes{
			Details: map[string]*commonpb.Payloads{"k": {Payloads: []*commonpb.Payload{makeTestPayload(1)}}},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("UpsertWorkflowSearchAttributesCommandAttributes error when IndexedFields exceed error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		// size = len("k") + len(data) = 1 + 200 = 201
		msg := &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
			SearchAttributes: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{"k": makeTestPayload(200)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("UpsertWorkflowSearchAttributesCommandAttributes warning when IndexedFields exceed warning limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		msg := &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
			SearchAttributes: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{"k": makeTestPayload(200)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasWarningLine(logger))
	})

	t.Run("UpsertWorkflowSearchAttributesCommandAttributes child payloads no error and warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &commandpb.UpsertWorkflowSearchAttributesCommandAttributes{
			SearchAttributes: &commonpb.SearchAttributes{
				IndexedFields: map[string]*commonpb.Payload{"k": makeTestPayload(1)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("ModifyWorkflowPropertiesCommandAttributes error when UpsertedMemo.Fields exceed error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &commandpb.ModifyWorkflowPropertiesCommandAttributes{
			UpsertedMemo: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{"k": makeTestPayload(200)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("ModifyWorkflowPropertiesCommandAttributes warning when UpsertedMemo.Fields exceed warning limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		msg := &commandpb.ModifyWorkflowPropertiesCommandAttributes{
			UpsertedMemo: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{"k": makeTestPayload(200)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasWarningLine(logger))
	})

	t.Run("ModifyWorkflowPropertiesCommandAttributes child payloads no error and warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &commandpb.ModifyWorkflowPropertiesCommandAttributes{
			UpsertedMemo: &commonpb.Memo{
				Fields: map[string]*commonpb.Payload{"k": makeTestPayload(1)},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	for _, tc := range []struct {
		name        string
		makeMsg     func() proto.Message
		assertField func(t *testing.T, msg proto.Message)
	}{
		{
			name: "WorkflowQueryResult",
			makeMsg: func() proto.Message {
				return &querypb.WorkflowQueryResult{
					Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
				}
			},
			assertField: func(t *testing.T, msg proto.Message) {
				m := msg.(*querypb.WorkflowQueryResult)
				require.Nil(t, m.Answer)
				require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, m.ResultType)
				require.NotEmpty(t, m.ErrorMessage)
			},
		},
		{
			name: "RespondQueryTaskCompletedRequest",
			makeMsg: func() proto.Message {
				return &workflowservice.RespondQueryTaskCompletedRequest{
					QueryResult: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
				}
			},
			assertField: func(t *testing.T, msg proto.Message) {
				m := msg.(*workflowservice.RespondQueryTaskCompletedRequest)
				require.Nil(t, m.QueryResult)
				require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, m.CompletedType)
				require.NotEmpty(t, m.ErrorMessage)
			},
		},
	} {
		tc := tc
		t.Run(tc.name+" transforms result when payload exceeds error limit", func(t *testing.T) {
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
			setErrorLimits(&payloadLimits{payloadSize: 10})
			msg := tc.makeMsg()
			err := visitProtoPayloads(context.Background(), visitor, msg, 0)
			require.NoError(t, err)
			tc.assertField(t, msg)
		})
		t.Run(tc.name+" warning when payload exceeds warning limit", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			msg := tc.makeMsg()
			err := visitProtoPayloads(context.Background(), visitor, msg, 0)
			require.NoError(t, err)
			require.True(t, hasWarningLine(logger))
		})
		t.Run(tc.name+" child payloads skip error and warning", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			setErrorLimits(&payloadLimits{payloadSize: 10})
			msg := tc.makeMsg()
			err := visitProtoPayloads(context.Background(), visitor, msg, 0)
			require.NoError(t, err)
			require.Empty(t, logger.Lines())
		})
	}

	skipErrorOnlyTypes := []struct {
		name string
		msg  proto.Message
	}{
		{"RespondActivityTaskFailedRequest", &workflowservice.RespondActivityTaskFailedRequest{
			LastHeartbeatDetails: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
		}},
		{"RespondActivityTaskFailedByIdRequest", &workflowservice.RespondActivityTaskFailedByIdRequest{
			LastHeartbeatDetails: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
		}},
		{"RespondWorkflowTaskFailedRequest", &workflowservice.RespondWorkflowTaskFailedRequest{
			Failure: &failurepb.Failure{
				FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
					ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
						Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
					},
				},
			},
		}},
	}

	for _, tc := range skipErrorOnlyTypes {
		tc := tc
		t.Run(tc.name+" skips payload and memo error limits but not warning", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			setErrorLimits(&payloadLimits{payloadSize: 10, memoSize: 10})
			err := visitProtoPayloads(context.Background(), visitor, tc.msg, 0)
			require.NoError(t, err)
			require.True(t, hasWarningLine(logger))
		})
	}
}

func TestCreateScheduleRequestSpecialization(t *testing.T) {
	makeScheduleRequest := func(memoSize, inputSize int) *workflowservice.CreateScheduleRequest {
		return &workflowservice.CreateScheduleRequest{
			Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(memoSize)}},
			Schedule: &schedulepb.Schedule{
				Action: &schedulepb.ScheduleAction{
					Action: &schedulepb.ScheduleAction_StartWorkflow{
						StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
							Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(inputSize)}},
						},
					},
				},
			},
		}
	}

	t.Run("error when combined memo+input exceeds payload error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 100})
		msg := makeScheduleRequest(60, 60)
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("warning when combined memo+input exceeds payload warning limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10, memoSize: 10000}, logger)
		msg := makeScheduleRequest(60, 60)
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasWarningLine(logger))
	})

	t.Run("no memo size check fires", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10000, memoSize: 10})
		msg := makeScheduleRequest(200, 1)
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.False(t, hasMemoWarningLine(logger))
	})

	t.Run("non-StartWorkflow action skips combined check", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{payloadSize: 1})
		msg := &workflowservice.CreateScheduleRequest{
			Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(200)}},
			Schedule: &schedulepb.Schedule{
				Action: &schedulepb.ScheduleAction{},
			},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
	})
}

func hasMemoWarningLine(logger *ilog.MemoryLogger) bool {
	return slices.ContainsFunc(logger.Lines(), func(line string) bool {
		return strings.Contains(line, "WARN  [TMPRL1103] Attempted to upload memo with size that exceeded the warning limit.")
	})
}

func TestMemoLimitsVisitorWarning(t *testing.T) {
	makeMemo := func(payloadSize int) *commonpb.Memo {
		return &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(payloadSize)}}
	}

	t.Run("warning when aggregate memo size exceeds limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(200), 0)
		require.NoError(t, err)
		require.True(t, hasMemoWarningLine(logger))
	})

	t.Run("no warning when aggregate memo size is under limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, logger)
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(10), 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("zero memo warning limit disables memo warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 0}, logger)
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(10000), 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("memo warning does not trigger payload warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		// memo limit low, payload limit high — only memo warning should fire
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(200), 0)
		require.NoError(t, err)
		require.True(t, hasMemoWarningLine(logger))
		require.False(t, hasWarningLine(logger))
	})

	t.Run("fires for StartWorkflowExecutionRequest memo", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		msg := &workflowservice.StartWorkflowExecutionRequest{
			Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(200)}},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasMemoWarningLine(logger))
	})

	t.Run("UpdateScheduleRequest memo skips error but not warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		setErrorLimits(&payloadLimits{memoSize: 10})
		msg := &workflowservice.UpdateScheduleRequest{
			Memo: &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(200)}},
		}
		err := visitProtoPayloads(context.Background(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasMemoWarningLine(logger))
	})
}

func TestMemoLimitsVisitorError(t *testing.T) {
	makeMemo := func(payloadSize int) *commonpb.Memo {
		return &commonpb.Memo{Fields: map[string]*commonpb.Payload{"k": makeTestPayload(payloadSize)}}
	}

	t.Run("error when aggregate memo size exceeds error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{memoSize: 10})
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(200), 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "memo")
		require.Equal(t, int64(10), pse.limit)
	})

	t.Run("no error when memo size is under error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{memoSize: 10000})
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(10), 0)
		require.NoError(t, err)
	})

	t.Run("zero memo error limit means no error check", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{memoSize: 0})
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(100000), 0)
		require.NoError(t, err)
	})

	t.Run("memo error does not trigger payload error", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		// memo error limit low, payload error limit high
		setErrorLimits(&payloadLimits{payloadSize: 100000, memoSize: 10})
		err := visitProtoPayloads(context.Background(), visitor, makeMemo(200), 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "memo")
	})
}
