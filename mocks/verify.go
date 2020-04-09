package mocks

import (
	"go.temporal.io/temporal/client"
)

// make sure mocks are in sync with interfaces
var _ client.Client = (*Client)(nil)
var _ client.NamespaceClient = (*NamespaceClient)(nil)
