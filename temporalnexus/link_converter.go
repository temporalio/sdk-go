package temporalnexus

import (
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"

	"go.temporal.io/sdk/internal"
)

// ConvertLinkWorkflowEventToNexusLink converts a Link_WorkflowEvent type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkWorkflowEventToNexusLink(we *commonpb.Link_WorkflowEvent) nexus.Link {
	return internal.ConvertLinkWorkflowEventToNexusLink(we)
}

// ConvertNexusLinkToLinkWorkflowEvent converts a Nexus Link to Link_WorkflowEvent.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkWorkflowEvent(link nexus.Link) (*commonpb.Link_WorkflowEvent, error) {
	return internal.ConvertNexusLinkToLinkWorkflowEvent(link)
}

// ConvertLinkNexusOperationToNexusLink converts a Link_NexusOperation type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkNexusOperationToNexusLink(no *commonpb.Link_NexusOperation) nexus.Link {
	return internal.ConvertLinkNexusOperationToNexusLink(no)
}

// ConvertNexusLinkToLinkNexusOperation converts a Nexus Link to Link_NexusOperation.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkNexusOperation(link nexus.Link) (*commonpb.Link_NexusOperation, error) {
	return internal.ConvertNexusLinkToLinkNexusOperation(link)
}
