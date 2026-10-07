package temporalnexus

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/nexus-rpc/sdk-go/nexus"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	ilog "go.temporal.io/sdk/internal/log"
	"google.golang.org/protobuf/testing/protocmp"
)

func nexusLinkFor(t *testing.T, linkType, rawURL string) nexus.Link {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return nexus.Link{URL: u, Type: linkType}
}

// convertNexusLinks turns the links a caller attached to a Nexus request into common.v1.Link so
// the handler's own RPCs can carry them. A link it cannot convert fails the operation, while a
// link type it does not know is skipped, so both paths are pinned here.
func TestConvertNexusLinks(t *testing.T) {
	workflowEventURL := "temporal:///namespaces/ns/workflows/wf-id/run-id/history" +
		"?referenceType=EventReference&eventID=1&eventType=WorkflowExecutionStarted"
	activityURL := "temporal:///namespaces/ns/activities/act-id/run-id/details"
	nexusOperationURL := "temporal:///namespaces/ns/nexus-operations/op-id/run-id/details"
	workflowURL := "temporal:///namespaces/ns/workflows/wf-id/run-id"

	workflowEventType := string((&commonpb.Link_WorkflowEvent{}).ProtoReflect().Descriptor().FullName())
	activityType := string((&commonpb.Link_Activity{}).ProtoReflect().Descriptor().FullName())
	nexusOperationType := string((&commonpb.Link_NexusOperation{}).ProtoReflect().Descriptor().FullName())
	workflowType := string((&commonpb.Link_Workflow{}).ProtoReflect().Descriptor().FullName())

	wantWorkflowEvent := &commonpb.Link{
		Variant: &commonpb.Link_WorkflowEvent_{
			WorkflowEvent: &commonpb.Link_WorkflowEvent{
				Namespace:  "ns",
				WorkflowId: "wf-id",
				RunId:      "run-id",
				Reference: &commonpb.Link_WorkflowEvent_EventRef{
					EventRef: &commonpb.Link_WorkflowEvent_EventReference{
						EventId:   1,
						EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
					},
				},
			},
		},
	}
	wantActivity := &commonpb.Link{
		Variant: &commonpb.Link_Activity_{
			Activity: &commonpb.Link_Activity{Namespace: "ns", ActivityId: "act-id", RunId: "run-id"},
		},
	}
	wantWorkflow := &commonpb.Link{
		Variant: &commonpb.Link_Workflow_{
			Workflow: &commonpb.Link_Workflow{Namespace: "ns", WorkflowId: "wf-id", RunId: "run-id"},
		},
	}
	wantNexusOperation := &commonpb.Link{
		Variant: &commonpb.Link_NexusOperation_{
			NexusOperation: &commonpb.Link_NexusOperation{Namespace: "ns", OperationId: "op-id", RunId: "run-id"},
		},
	}

	for _, tc := range []struct {
		name      string
		input     []nexus.Link
		want      []*commonpb.Link
		wantError string
	}{
		{
			name:  "empty",
			input: nil,
			want:  nil,
		},
		{
			name:  "workflow event",
			input: []nexus.Link{nexusLinkFor(t, workflowEventType, workflowEventURL)},
			want:  []*commonpb.Link{wantWorkflowEvent},
		},
		{
			name:  "activity",
			input: []nexus.Link{nexusLinkFor(t, activityType, activityURL)},
			want:  []*commonpb.Link{wantActivity},
		},
		{
			name:  "nexus operation",
			input: []nexus.Link{nexusLinkFor(t, nexusOperationType, nexusOperationURL)},
			want:  []*commonpb.Link{wantNexusOperation},
		},
		{
			// Order is preserved, and each variant is wrapped in its matching oneof.
			name: "all supported types in order",
			input: []nexus.Link{
				nexusLinkFor(t, nexusOperationType, nexusOperationURL),
				nexusLinkFor(t, workflowEventType, workflowEventURL),
				nexusLinkFor(t, activityType, activityURL),
				nexusLinkFor(t, workflowType, workflowURL),
			},
			want: []*commonpb.Link{wantNexusOperation, wantWorkflowEvent, wantActivity, wantWorkflow},
		},
		{
			// A link type this function does not handle is dropped rather than failing the
			// operation, because links are not essential to it.
			name:  "unknown link type is skipped",
			input: []nexus.Link{nexusLinkFor(t, "temporal.api.common.v1.Link.NotAVariant", workflowEventURL)},
			want:  nil,
		},
		{
			// A Workflow link is what a caller sends when it has no history event to point at,
			// such as a query or an update rejected during validation.
			name:  "workflow",
			input: []nexus.Link{nexusLinkFor(t, workflowType, workflowURL)},
			want:  []*commonpb.Link{wantWorkflow},
		},
		{
			// The reason the server attaches to a Workflow link has to survive the conversion.
			name:  "workflow with reason",
			input: []nexus.Link{nexusLinkFor(t, workflowType, workflowURL+"?reason=Query+processed")},
			want: []*commonpb.Link{{
				Variant: &commonpb.Link_Workflow_{
					Workflow: &commonpb.Link_Workflow{
						Namespace: "ns", WorkflowId: "wf-id", RunId: "run-id", Reason: "Query processed",
					},
				},
			}},
		},
		{
			// An unhandled type does not stop the links around it from converting.
			name: "unknown type does not drop its neighbours",
			input: []nexus.Link{
				nexusLinkFor(t, "temporal.api.common.v1.Link.NotAVariant", workflowEventURL),
				nexusLinkFor(t, activityType, activityURL),
			},
			want: []*commonpb.Link{wantActivity},
		},
		{
			// A link whose type IS handled but whose URL will not parse fails the whole call,
			// which the callers surface as a BadRequest handler error.
			name:      "malformed link of a handled type is an error",
			input:     []nexus.Link{nexusLinkFor(t, workflowEventType, "temporal:///namespaces/ns/workflows/wf-id")},
			wantError: "failed to parse link to Link_WorkflowEvent",
		},
		{
			name:      "wrong scheme is an error",
			input:     []nexus.Link{nexusLinkFor(t, activityType, "https:///namespaces/ns/activities/act-id/run-id/details")},
			wantError: "invalid scheme",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertNexusLinks(tc.input, ilog.NewNopLogger())
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Empty(t, cmp.Diff(tc.want, got, protocmp.Transform()))
		})
	}
}
