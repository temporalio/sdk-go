package mocks

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// make sure mocks are in sync with interfaces
var (
	_ client.Client               = (*Client)(nil)
	_ client.HistoryEventIterator = (*HistoryEventIterator)(nil)
	_ client.NamespaceClient      = (*NamespaceClient)(nil)
	_ converter.EncodedValue      = (*EncodedValue)(nil)
	_ client.WorkflowRun          = (*WorkflowRun)(nil)
)
