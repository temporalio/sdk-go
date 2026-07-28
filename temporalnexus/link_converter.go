package temporalnexus

import (
	"github.com/nexus-rpc/sdk-go/nexus"
	commonpb "go.temporal.io/api/common/v1"
	apinexus "go.temporal.io/api/temporalnexus"
)

// ConvertLinkWorkflowEventToNexusLink converts a Link_WorkflowEvent type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkWorkflowEventToNexusLink(we *commonpb.Link_WorkflowEvent) nexus.Link {
	return apinexus.ConvertLinkWorkflowEventToNexusLink(we)
}

// ConvertNexusLinkToLinkWorkflowEvent converts a Nexus Link to Link_WorkflowEvent.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkWorkflowEvent(link nexus.Link) (*commonpb.Link_WorkflowEvent, error) {
	return apinexus.ConvertNexusLinkToLinkWorkflowEvent(link)
}

// ConvertLinkNexusOperationToNexusLink converts a Link_NexusOperation type to Nexus Link.
//
// NOTE: Experimental
func ConvertLinkNexusOperationToNexusLink(no *commonpb.Link_NexusOperation) nexus.Link {
	return apinexus.ConvertLinkNexusOperationToNexusLink(no)
}

// ConvertNexusLinkToLinkNexusOperation converts a Nexus Link to Link_NexusOperation.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkNexusOperation(link nexus.Link) (*commonpb.Link_NexusOperation, error) {
	return apinexus.ConvertNexusLinkToLinkNexusOperation(link)
}

// ConvertWorkflowLinkToNexusLink converts a Link_Workflow to a Nexus Link.
//
// NOTE: Experimental
func ConvertWorkflowLinkToNexusLink(workflowLink *commonpb.Link_Workflow) nexus.Link {
	return apinexus.ConvertWorkflowLinkToNexusLink(workflowLink)
}

// ConvertCommonLinkToNexusLink converts a Common Link to a Nexus Link. Will be used
// to safely point to non-workflow event links for cases where Nexus operations fail.
// Eg. UpdateWorkflow fails validation -> point to Workflow instead of WorkflowEvent
// as there will not be a history event. Returns an empty link if commonLink is neither
// a WorkflowEventLink nor a WorkflowLink
//
// NOTE: Experimental
func ConvertCommonLinkToNexusLink(commonLink *commonpb.Link) nexus.Link {
	switch commonLink.GetVariant().(type) {
	case *commonpb.Link_WorkflowEvent_:
		return ConvertLinkWorkflowEventToNexusLink(commonLink.GetWorkflowEvent())
	case *commonpb.Link_Workflow_:
		return ConvertWorkflowLinkToNexusLink(commonLink.GetWorkflow())
	default:
		return nexus.Link{}
	}
}

// ConvertLinkActivityToNexusLink converts a Link_Activity type to a Nexus Link.
//
// NOTE: Experimental
func ConvertLinkActivityToNexusLink(a *commonpb.Link_Activity) nexus.Link {
	return apinexus.ConvertLinkActivityToNexusLink(a)
}

// ConvertNexusLinkToLinkActivity converts a Nexus Link back to a Link_Activity.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkActivity(link nexus.Link) (*commonpb.Link_Activity, error) {
	return apinexus.ConvertNexusLinkToLinkActivity(link)
}
