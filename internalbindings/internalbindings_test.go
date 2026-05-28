// Tests covering the publicly exposed Nexus caller-side bindings added so
// that non-Go SDKs (e.g. roadrunner-temporal proxying for PHP) can build
// `ExecuteNexusOperationParams` from outside the `internal` package.
//
// The constructor is the only way for an external caller to populate the
// struct — its fields stay unexported on purpose. These tests document and
// guard that contract.
package internalbindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
)

func TestNewNexusClient_ExposesEndpointAndService(t *testing.T) {
	c := NewNexusClient("ep-1", "svc-1")
	require.NotNil(t, c, "client constructor must return a non-nil NexusClient")

	// NexusClient is an interface from go.temporal.io/sdk/internal — it
	// exposes Endpoint() and Service() readers. We assert through the
	// re-exported alias so the test breaks loudly if the interface shape
	// changes upstream and the alias drifts.
	assert.Equal(t, "ep-1", c.Endpoint())
	assert.Equal(t, "svc-1", c.Service())
}

func TestNewExecuteNexusOperationParams_RoundtripsAllFields(t *testing.T) {
	// The struct's fields are unexported, so the only contract surface
	// is "what you put in is what dispatchers see when they call
	// WorkflowEnvironment.ExecuteNexusOperation". We can't read fields
	// directly, but we can verify the constructor accepts the documented
	// inputs without panicking and returns a value of the public type.
	client := NewNexusClient("ep-2", "svc-2")
	payload := &commonpb.Payload{Data: []byte("hello")}
	options := NexusOperationOptions{}
	headers := map[string]string{"Nexus-Operation-Token": "tok-x"}

	params := NewExecuteNexusOperationParams(client, "op-1", payload, options, headers)

	// The exported type alias must match the type returned by the
	// constructor — protects against accidental drift between the
	// `type ExecuteNexusOperationParams = internal....` line and the
	// `func NewExecuteNexusOperationParams ... ExecuteNexusOperationParams`
	// line.
	var _ ExecuteNexusOperationParams = params
}

func TestNewExecuteNexusOperationParams_AcceptsNilHeader(t *testing.T) {
	// Headers map is optional — Java callers that don't surface a Nexus
	// header just pass nil. Ensure nil doesn't panic on construction.
	require.NotPanics(t, func() {
		NewExecuteNexusOperationParams(
			NewNexusClient("ep", "svc"),
			"op",
			nil, // input
			NexusOperationOptions{},
			nil, // header
		)
	})
}
