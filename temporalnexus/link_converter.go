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
	return apinexus.ConvertLinkWorkflowToNexusLink(workflowLink)
}

// ConvertNexusLinkToLinkWorkflow converts a Nexus Link back to a Link_Workflow.
//
// NOTE: Experimental
func ConvertNexusLinkToLinkWorkflow(link nexus.Link) (*commonpb.Link_Workflow, error) {
	return apinexus.ConvertNexusLinkToLinkWorkflow(link)
}

// ConvertCommonLinkToNexusLink converts a Common Link to a Nexus Link, dispatching on the
// populated variant. A Workflow link is how a Nexus operation points at an execution when there
// is no history event to reference, such as an UpdateWorkflow that fails validation. Returns the
// zero Link if no variant is set, or if the variant has no Nexus link form.
//
// NOTE: Experimental
func ConvertCommonLinkToNexusLink(commonLink *commonpb.Link) nexus.Link {
	switch commonLink.GetVariant().(type) {
	case *commonpb.Link_WorkflowEvent_:
		return ConvertLinkWorkflowEventToNexusLink(commonLink.GetWorkflowEvent())
	case *commonpb.Link_Workflow_:
		return ConvertWorkflowLinkToNexusLink(commonLink.GetWorkflow())
	case *commonpb.Link_NexusOperation_:
		return ConvertLinkNexusOperationToNexusLink(commonLink.GetNexusOperation())
	case *commonpb.Link_Activity_:
		return ConvertLinkActivityToNexusLink(commonLink.GetActivity())
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
