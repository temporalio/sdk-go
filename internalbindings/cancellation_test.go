package internalbindings_test

import (
	"go.temporal.io/api/sdk/v1"
	"go.temporal.io/sdk/internalbindings"
)

var _ interface {
	GetCancellationReason() string
	RegisterCancelHandler(handler func())
	RequestCancelChildWorkflow(namespace, workflowID, reason string)
	RequestCancelExternalWorkflow(namespace, workflowID, runID, reason string, eventGroupMarkers []*sdk.EventGroupMarker, callback internalbindings.ResultHandler)
} = internalbindings.WorkflowEnvironment(nil)
