package internal

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	commandpb "go.temporal.io/api/command/v1"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	nexuspb "go.temporal.io/api/nexus/v1"
	"go.temporal.io/api/proxy"
	querypb "go.temporal.io/api/query/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/internal/extstore"
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		t.Run(tc.name+" transforms result when payload exceeds error limit", func(t *testing.T) {
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000}, nil)
			setErrorLimits(&payloadLimits{payloadSize: 10})
			msg := tc.makeMsg()
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)
			tc.assertField(t, msg)
		})
		t.Run(tc.name+" warning when payload exceeds warning limit", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			msg := tc.makeMsg()
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)
			require.True(t, hasWarningLine(logger))
		})
		t.Run(tc.name+" degraded result emits no warning", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			setErrorLimits(&payloadLimits{payloadSize: 10})
			msg := tc.makeMsg()
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		{"RespondNexusTaskFailedRequest", &workflowservice.RespondNexusTaskFailedRequest{
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
		t.Run(tc.name+" skips payload and memo error limits but not warning", func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			setErrorLimits(&payloadLimits{payloadSize: 10, memoSize: 10})
			err := visitProtoPayloads(t.Context(), visitor, tc.msg, 0)
			require.NoError(t, err)
			require.True(t, hasWarningLine(logger))
		})
	}

	// A Nexus task completion is not size-checked at all: an operation-error failure
	// produces neither an error nor a warning (the sync result payload is likewise
	// skipped as a single-payload field).
	t.Run("RespondNexusTaskCompletedRequest skips payload error and warning limits", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10})
		msg := &workflowservice.RespondNexusTaskCompletedRequest{
			Response: &nexuspb.Response{
				Variant: &nexuspb.Response_StartOperation{
					StartOperation: &nexuspb.StartOperationResponse{
						Variant: &nexuspb.StartOperationResponse_Failure{
							Failure: &failurepb.Failure{
								FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
									ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
										Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
									},
								},
							},
						},
					},
				},
			},
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})
}

// countingStorageDriver is a minimal in-memory extstore.StorageDriver used to
// confirm that oversized query results are actually offloaded (Store called,
// result payload becomes an external storage reference) rather than merely not
// erroring. Store is called concurrently by
// TestPayloadLimitsVisitorQueryResultConcurrentVisit's offload subtest, so
// storeCount and data are guarded by mu rather than left as plain fields.
type countingStorageDriver struct {
	mu         sync.Mutex
	storeCount int
	data       map[string]*commonpb.Payload
}

func newCountingStorageDriver() *countingStorageDriver {
	return &countingStorageDriver{data: map[string]*commonpb.Payload{}}
}

func (d *countingStorageDriver) Name() string { return "counting" }
func (d *countingStorageDriver) Type() string { return "counting" }

func (d *countingStorageDriver) Store(_ extstore.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]extstore.StorageDriverClaim, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeCount++
	claims := make([]extstore.StorageDriverClaim, len(payloads))
	for i, p := range payloads {
		key := fmt.Sprintf("k%d", len(d.data))
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = extstore.StorageDriverClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *countingStorageDriver) Retrieve(_ extstore.StorageDriverRetrieveContext, claims []extstore.StorageDriverClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		out[i] = d.data[c.ClaimData["key"]]
	}
	return out, nil
}

func (d *countingStorageDriver) storeCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.storeCount
}

// decliningSelector is an extstore.StorageDriverSelector that always leaves the
// payload inline, simulating a driver selector that declines to store it.
type decliningSelector struct{}

func (decliningSelector) SelectDriver(extstore.StorageDriverSelectContext, *commonpb.Payload) (extstore.StorageDriver, error) {
	return nil, nil
}

// erroringStorageDriver is an extstore.StorageDriver whose Store always fails,
// used to confirm that a driver failure while offloading an oversized query
// result surfaces as an error from visitProtoPayloads instead of being
// silently absorbed into a degraded query result.
type erroringStorageDriver struct{}

func (erroringStorageDriver) Name() string { return "erroring" }
func (erroringStorageDriver) Type() string { return "erroring" }

func (erroringStorageDriver) Store(extstore.StorageDriverStoreContext, []*commonpb.Payload) ([]extstore.StorageDriverClaim, error) {
	return nil, errors.New("store failed")
}

func (erroringStorageDriver) Retrieve(extstore.StorageDriverRetrieveContext, []extstore.StorageDriverClaim) ([]*commonpb.Payload, error) {
	return nil, errors.New("retrieve failed")
}

// newTestOutboundVisitor builds the same [extstore visitor, payload limits visitor]
// composite chain the worker and client wire up in internal_worker.go and
// internal_workflow_client.go, so these tests exercise the real interaction
// between external storage and the size-limit visitor rather than the limits
// visitor in isolation.
func newTestOutboundVisitor(t *testing.T, storage extstore.ExternalStorage, errorLimit int64) PayloadVisitor {
	t.Helper()
	params, err := extstore.ExternalStorageToParams(storage)
	require.NoError(t, err)
	limitsVisitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 1_000_000}, nil)
	setErrorLimits(&payloadLimits{payloadSize: errorLimit})
	return newCompositePayloadVisitor(extstore.NewExternalStorageVisitor(params), limitsVisitor)
}

// TestPayloadLimitsVisitorQueryResultExternalStorage exercises the interaction
// between external storage and the size-limit visitor for query results. See
// the *querypb.WorkflowQueryResult and
// *workflowservice.RespondQueryTaskCompletedRequest cases in ContextHook and the
// ctx.Parent switch in Visit.
func TestPayloadLimitsVisitorQueryResultExternalStorage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		makeMsg       func(p *commonpb.Payload) proto.Message
		makeFailed    func(f *failurepb.Failure) proto.Message
		resultPayload func(msg proto.Message) *commonpb.Payload
		isFailed      func(msg proto.Message) bool
		errMessage    func(msg proto.Message) string
	}{
		{
			name: "WorkflowQueryResult",
			makeMsg: func(p *commonpb.Payload) proto.Message {
				return &querypb.WorkflowQueryResult{Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{p}}}
			},
			makeFailed: func(f *failurepb.Failure) proto.Message {
				return &querypb.WorkflowQueryResult{Failure: f}
			},
			resultPayload: func(msg proto.Message) *commonpb.Payload {
				answer := msg.(*querypb.WorkflowQueryResult).GetAnswer().GetPayloads()
				if len(answer) == 0 {
					return nil
				}
				return answer[0]
			},
			isFailed: func(msg proto.Message) bool {
				return msg.(*querypb.WorkflowQueryResult).GetResultType() == enumspb.QUERY_RESULT_TYPE_FAILED
			},
			errMessage: func(msg proto.Message) string {
				return msg.(*querypb.WorkflowQueryResult).GetErrorMessage()
			},
		},
		{
			name: "RespondQueryTaskCompletedRequest",
			makeMsg: func(p *commonpb.Payload) proto.Message {
				return &workflowservice.RespondQueryTaskCompletedRequest{QueryResult: &commonpb.Payloads{Payloads: []*commonpb.Payload{p}}}
			},
			makeFailed: func(f *failurepb.Failure) proto.Message {
				return &workflowservice.RespondQueryTaskCompletedRequest{Failure: f}
			},
			resultPayload: func(msg proto.Message) *commonpb.Payload {
				result := msg.(*workflowservice.RespondQueryTaskCompletedRequest).GetQueryResult().GetPayloads()
				if len(result) == 0 {
					return nil
				}
				return result[0]
			},
			isFailed: func(msg proto.Message) bool {
				return msg.(*workflowservice.RespondQueryTaskCompletedRequest).GetCompletedType() == enumspb.QUERY_RESULT_TYPE_FAILED
			},
			errMessage: func(msg proto.Message) string {
				return msg.(*workflowservice.RespondQueryTaskCompletedRequest).GetErrorMessage()
			},
		},
	} {
		// bigPayloadSize wraps to ~2006 bytes as a *commonpb.Payloads, comfortably
		// above midErrorLimit; an offloaded storage reference for it is ~150 bytes,
		// comfortably below midErrorLimit. This margin is what lets these tests
		// distinguish the raw payload size (trips the error limit) from the
		// offloaded reference size (does not).
		const bigPayloadSize = 2000
		const midErrorLimit = int64(500)

		t.Run(tc.name+" offloads to external storage instead of failing when over the error limit", func(t *testing.T) {
			driver := newCountingStorageDriver()
			visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{
				Drivers:              []extstore.StorageDriver{driver},
				PayloadSizeThreshold: 10,
			}, midErrorLimit)

			msg := tc.makeMsg(makeTestPayload(bigPayloadSize))
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)

			require.False(t, tc.isFailed(msg), "query result should not degrade once external storage has offloaded it")
			require.Empty(t, tc.errMessage(msg))
			require.Equal(t, 1, driver.storeCalls(), "driver.Store should be called for the oversized result")
			p := tc.resultPayload(msg)
			require.NotNil(t, p)
			require.True(t, extstore.IsStorageReference(p), "result payload should be replaced with a storage reference")
		})

		t.Run(tc.name+" degrades to a failed result when the driver selector declines the payload", func(t *testing.T) {
			driver := newCountingStorageDriver()
			visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{
				Drivers:              []extstore.StorageDriver{driver},
				DriverSelector:       decliningSelector{},
				PayloadSizeThreshold: 10,
			}, midErrorLimit)

			msg := tc.makeMsg(makeTestPayload(bigPayloadSize))
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)

			require.True(t, tc.isFailed(msg))
			require.NotEmpty(t, tc.errMessage(msg))
			require.Equal(t, 0, driver.storeCalls())
			require.Nil(t, tc.resultPayload(msg))
		})

		t.Run(tc.name+" no driver configured still degrades to a failed result", func(t *testing.T) {
			// No StorageDriver at all: extstore.NewExternalStorageVisitor is a
			// pass-through (this is what client.Options.ExternalStorage's zero
			// value produces), so the raw size is checked directly.
			visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{}, midErrorLimit)

			msg := tc.makeMsg(makeTestPayload(bigPayloadSize))
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)

			require.True(t, tc.isFailed(msg))
			require.NotEmpty(t, tc.errMessage(msg))
			require.Nil(t, tc.resultPayload(msg))
		})

		t.Run(tc.name+" storage driver failure surfaces as an error instead of degrading the result", func(t *testing.T) {
			// The storage driver failure happens inside the external storage
			// visitor, which runs before the limits visitor in the composite
			// chain (see newTestOutboundVisitor); the limits visitor's
			// query-result branch never runs, so the message is left as-is
			// rather than degraded to a failed query result.
			visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{
				Drivers:              []extstore.StorageDriver{erroringStorageDriver{}},
				PayloadSizeThreshold: 10,
			}, midErrorLimit)

			msg := tc.makeMsg(makeTestPayload(bigPayloadSize))
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.Error(t, err)
			require.Contains(t, err.Error(), "store failed")

			require.False(t, tc.isFailed(msg))
			require.Empty(t, tc.errMessage(msg))
			p := tc.resultPayload(msg)
			require.NotNil(t, p)
			require.False(t, extstore.IsStorageReference(p))
		})

		t.Run(tc.name+" oversized Failure details are not size-checked", func(t *testing.T) {
			// The Failure field is a sibling of Answer/QueryResult on the same
			// message; it must keep today's limitCheckNone exemption and not be
			// picked up by the query-result branch in Visit.
			logger := ilog.NewMemoryLogger()
			visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10}, logger)
			setErrorLimits(&payloadLimits{payloadSize: 10})
			msg := tc.makeFailed(&failurepb.Failure{
				FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
					ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
						Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
					},
				},
			})
			err := visitProtoPayloads(t.Context(), visitor, msg, 0)
			require.NoError(t, err)
			require.False(t, tc.isFailed(msg))
			require.Empty(t, logger.Lines())
		})
	}

	t.Run("RespondWorkflowTaskCompletedRequest.QueryResults offloads nested query results", func(t *testing.T) {
		driver := newCountingStorageDriver()
		visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{
			Drivers:              []extstore.StorageDriver{driver},
			PayloadSizeThreshold: 10,
		}, 500)

		msg := &workflowservice.RespondWorkflowTaskCompletedRequest{
			QueryResults: map[string]*querypb.WorkflowQueryResult{
				"q1": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}},
			},
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
		require.NoError(t, err)

		result := msg.QueryResults["q1"]
		require.Equal(t, enumspb.QUERY_RESULT_TYPE_UNSPECIFIED, result.GetResultType())
		require.Empty(t, result.GetErrorMessage())
		require.Equal(t, 1, driver.storeCalls())
		require.True(t, extstore.IsStorageReference(result.GetAnswer().GetPayloads()[0]))
	})
}

// TestPayloadLimitsVisitorQueryResultConcurrentVisit exercises failQueryResult
// under proxy.VisitPayloadsOptions.ConcurrencyLimit > 1, where the mutation of
// the query result and the visiting of the sibling Failure subtree can run in
// separate goroutines at the same time (see internal_task_pollers.go, which
// passes WorkerOptions.MaxConcurrentWorkflowTaskExternalStorageVisits as the
// concurrency limit for these same message types). Each case below sets both
// the result field and a Failure with payload-bearing details, so a run under
// -race would catch a data race between the two mutations.
func TestPayloadLimitsVisitorQueryResultConcurrentVisit(t *testing.T) {
	const errorLimit = int64(500)
	const concurrencyLimit = 8

	failureWithDetails := func() *failurepb.Failure {
		return &failurepb.Failure{
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
				ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
					Details: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(200)}},
				},
			},
		}
	}

	t.Run("WorkflowQueryResult", func(t *testing.T) {
		visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{}, errorLimit)
		msg := &querypb.WorkflowQueryResult{
			Answer:  &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}},
			Failure: failureWithDetails(),
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, concurrencyLimit)
		require.NoError(t, err)

		require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, msg.GetResultType())
		require.NotEmpty(t, msg.GetErrorMessage())
		require.Nil(t, msg.GetAnswer())
		require.Equal(t, int64(200), int64(len(msg.GetFailure().GetApplicationFailureInfo().GetDetails().GetPayloads()[0].GetData())))
	})

	t.Run("RespondQueryTaskCompletedRequest", func(t *testing.T) {
		visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{}, errorLimit)
		msg := &workflowservice.RespondQueryTaskCompletedRequest{
			QueryResult: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}},
			Failure:     failureWithDetails(),
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, concurrencyLimit)
		require.NoError(t, err)

		require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, msg.GetCompletedType())
		require.NotEmpty(t, msg.GetErrorMessage())
		require.Nil(t, msg.GetQueryResult())
		require.Equal(t, int64(200), int64(len(msg.GetFailure().GetApplicationFailureInfo().GetDetails().GetPayloads()[0].GetData())))
	})

	t.Run("RespondWorkflowTaskCompletedRequest.QueryResults, multiple results in flight", func(t *testing.T) {
		visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{}, errorLimit)
		msg := &workflowservice.RespondWorkflowTaskCompletedRequest{
			QueryResults: map[string]*querypb.WorkflowQueryResult{
				"q1": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
				"q2": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
				"q3": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
			},
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, concurrencyLimit)
		require.NoError(t, err)

		for name, result := range msg.QueryResults {
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_FAILED, result.GetResultType(), name)
			require.NotEmpty(t, result.GetErrorMessage(), name)
			require.Nil(t, result.GetAnswer(), name)
			require.Equal(t, int64(200), int64(len(result.GetFailure().GetApplicationFailureInfo().GetDetails().GetPayloads()[0].GetData())), name)
		}
	})

	t.Run("RespondWorkflowTaskCompletedRequest.QueryResults, concurrent offload to external storage", func(t *testing.T) {
		driver := newCountingStorageDriver()
		// Threshold sits between the 200-byte Failure details and the 2000-byte
		// Answer payloads, so only the oversized answers offload; storeCalls()
		// below would otherwise also count the three Failure details payloads.
		visitor := newTestOutboundVisitor(t, extstore.ExternalStorage{
			Drivers:              []extstore.StorageDriver{driver},
			PayloadSizeThreshold: 1000,
		}, errorLimit)
		msg := &workflowservice.RespondWorkflowTaskCompletedRequest{
			QueryResults: map[string]*querypb.WorkflowQueryResult{
				"q1": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
				"q2": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
				"q3": {Answer: &commonpb.Payloads{Payloads: []*commonpb.Payload{makeTestPayload(2000)}}, Failure: failureWithDetails()},
			},
		}
		err := visitProtoPayloads(t.Context(), visitor, msg, concurrencyLimit)
		require.NoError(t, err)

		require.Equal(t, 3, driver.storeCalls(), "each oversized answer should be offloaded rather than degraded")
		for name, result := range msg.QueryResults {
			require.Equal(t, enumspb.QUERY_RESULT_TYPE_UNSPECIFIED, result.GetResultType(), name)
			require.Empty(t, result.GetErrorMessage(), name)
			require.True(t, extstore.IsStorageReference(result.GetAnswer().GetPayloads()[0]), name)
			require.Equal(t, int64(200), int64(len(result.GetFailure().GetApplicationFailureInfo().GetDetails().GetPayloads()[0].GetData())), name)
		}
	})
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
	})

	t.Run("warning when combined memo+input exceeds payload warning limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10, memoSize: 10000}, logger)
		msg := makeScheduleRequest(60, 60)
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
		require.NoError(t, err)
		require.True(t, hasWarningLine(logger))
	})

	t.Run("no memo size check fires", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		setErrorLimits(&payloadLimits{payloadSize: 10000, memoSize: 10})
		msg := makeScheduleRequest(200, 1)
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(200), 0)
		require.NoError(t, err)
		require.True(t, hasMemoWarningLine(logger))
	})

	t.Run("no warning when aggregate memo size is under limit", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, logger)
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(10), 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("zero memo warning limit disables memo warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 0}, logger)
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(10000), 0)
		require.NoError(t, err)
		require.Empty(t, logger.Lines())
	})

	t.Run("memo warning does not trigger payload warning", func(t *testing.T) {
		logger := ilog.NewMemoryLogger()
		// memo limit low, payload limit high — only memo warning should fire
		visitor, _ := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10}, logger)
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(200), 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, msg, 0)
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
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(200), 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "memo")
		require.Equal(t, int64(10), pse.limit)
	})

	t.Run("no error when memo size is under error limit", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{memoSize: 10000})
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(10), 0)
		require.NoError(t, err)
	})

	t.Run("zero memo error limit means no error check", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		setErrorLimits(&payloadLimits{memoSize: 0})
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(100000), 0)
		require.NoError(t, err)
	})

	t.Run("memo error does not trigger payload error", func(t *testing.T) {
		visitor, setErrorLimits := newPayloadLimitsVisitor(payloadLimits{payloadSize: 10000, memoSize: 10000}, nil)
		// memo error limit low, payload error limit high
		setErrorLimits(&payloadLimits{payloadSize: 100000, memoSize: 10})
		err := visitProtoPayloads(t.Context(), visitor, makeMemo(200), 0)
		require.Error(t, err)
		var pse payloadSizeError
		require.ErrorAs(t, err, &pse)
		require.Contains(t, pse.Error(), "memo")
	})
}
