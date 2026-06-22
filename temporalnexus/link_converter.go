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
