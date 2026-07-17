package googleadk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/genai"

	"go.temporal.io/sdk/contrib/googleadk"
)

// schemaInner is a nested struct used to prove the json:"-" exclusion applies
// recursively through schemaFromType.
type schemaInner struct {
	Visible string `json:"visible"`
	Hidden  string `json:"-"`
}

// schemaArgs exercises every json-tag naming case the schema builder must
// honor: an explicit rename, an untagged field, a fully excluded field, a
// field literally named "-" (the json:"-," form), and a nested struct.
type schemaArgs struct {
	Query    string `json:"query"`
	Untagged string
	Secret   string      `json:"-"`
	Dash     string      `json:"-,"`
	Inner    schemaInner `json:"inner"`
}

// schemaArgsActivity is a signature-conforming activity whose TArgs drive the
// inferred tool schema; it is never executed by these tests.
func schemaArgsActivity(_ context.Context, in schemaArgs) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}

// declarer is the structural interface exposing the tool's function
// declaration. ADK's tool.Tool interface is minimal (Name/Description/
// IsLongRunning), so Declaration is reached via type assertion — the same
// pattern ADK itself uses.
type declarer interface {
	Declaration() *genai.FunctionDeclaration
}

// TestActivityAsToolSchemaExcludesJSONDashFields proves the inferred parameter
// schema follows encoding/json semantics: fields tagged json:"-" are never
// advertised to the model (the default data converter can never deliver them
// to the activity), including inside nested structs, while renaming, untagged
// fields, and the json:"-," literal-"-" name all keep working.
func TestActivityAsToolSchemaExcludesJSONDashFields(t *testing.T) {
	tl, err := googleadk.ActivityAsTool(schemaArgsActivity, googleadk.ActivityToolOptions{
		Name:        "schema-probe",
		Description: "schema probe",
	})
	require.NoError(t, err)

	d, ok := tl.(declarer)
	require.True(t, ok, "activity tool must expose Declaration()")
	require.NotNil(t, d.Declaration())
	require.NotNil(t, d.Declaration().Parameters)
	props := d.Declaration().Parameters.Properties

	// Naming behavior that must not regress.
	assert.Contains(t, props, "query", "json-renamed field advertised under its json name")
	assert.Contains(t, props, "Untagged", "untagged exported field advertised under its Go name")
	assert.Contains(t, props, "-", `json:"-," means a field literally named "-", not exclusion`)

	// The excluded field must not be advertised under any name.
	assert.NotContains(t, props, "Secret")
	assert.NotContains(t, props, "secret")

	// The exclusion applies recursively to nested structs.
	inner, ok := props["inner"]
	require.True(t, ok)
	require.NotNil(t, inner)
	assert.Contains(t, inner.Properties, "visible")
	assert.NotContains(t, inner.Properties, "Hidden")
}
