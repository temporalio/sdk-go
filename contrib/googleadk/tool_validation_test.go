package googleadk_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/workflow"

	"go.temporal.io/sdk/contrib/googleadk"
)

// validationArgs is the well-formed TArgs struct used by the signature
// validation tests.
type validationArgs struct {
	City string `json:"city"`
}

// otherArgs is a second struct so ctx-less two-struct functions have two
// distinct parameter types.
type otherArgs struct {
	Zip string `json:"zip"`
}

// TestActivityAsToolRejectsMissingContextArg proves a ctx-less two-struct
// function is rejected at construction. Before validation this shape was
// accepted and the schema was silently inferred from the WRONG struct (In(1))
// while the dispatched payload decoded into the first.
func TestActivityAsToolRejectsMissingContextArg(t *testing.T) {
	fn := func(validationArgs, otherArgs) error { return nil }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "must take context.Context as its first argument")
}

// TestActivityAsToolRejectsSingleInput proves the error for too few inputs
// reports the offending count.
func TestActivityAsToolRejectsSingleInput(t *testing.T) {
	fn := func(context.Context) (map[string]any, error) { return nil, nil }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "got 1 input")
}

// TestActivityAsToolRejectsExtraInputs proves functions with more than one
// TArgs parameter are rejected — Run dispatches exactly one args value, so
// extra parameters would be silently zero-filled at execution time.
func TestActivityAsToolRejectsExtraInputs(t *testing.T) {
	fn := func(context.Context, validationArgs, string) (map[string]any, error) { return nil, nil }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "got 3 input arguments")
}

// TestActivityAsToolRejectsWorkflowContextArg proves a workflow.Context first
// argument (a workflow function, not an activity) is rejected.
func TestActivityAsToolRejectsWorkflowContextArg(t *testing.T) {
	fn := func(workflow.Context, validationArgs) (map[string]any, error) { return nil, nil }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "context.Context as its first argument")
}

// richContext embeds context.Context: it implements it, but is a narrower
// type — the SDK invokes activities with a plain context.Context value, which
// cannot be assigned to a richContext parameter.
type richContext interface {
	context.Context
	SessionID() string
}

// TestActivityAsToolRejectsNarrowerContextArg proves the first argument must be
// exactly context.Context: a custom interface that embeds it passes an
// Implements check but would fail reflect.Call at dispatch, so it is rejected
// at construction instead.
func TestActivityAsToolRejectsNarrowerContextArg(t *testing.T) {
	fn := func(richContext, validationArgs) (map[string]any, error) { return nil, nil }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "context.Context as its first argument")
}

// TestActivityAsToolRejectsNoReturnValues proves a function with no return
// values is rejected — worker registration would panic on it anyway, but the
// tool constructor reports it immediately and with correct attribution.
func TestActivityAsToolRejectsNoReturnValues(t *testing.T) {
	fn := func(context.Context, validationArgs) {}
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "got 0 return values")
}

// TestActivityAsToolRejectsNonErrorLastReturn proves the last return value
// must implement error, matching Temporal's activity contract.
func TestActivityAsToolRejectsNonErrorLastReturn(t *testing.T) {
	fn := func(context.Context, validationArgs) (map[string]any, string) { return nil, "" }
	_, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{})
	require.ErrorContains(t, err, "must return error as its last return value")
}

// TestActivityAsToolAllowsErrorOnlyReturn guards the deliberate leniency on
// outputs: error-only side-effect activities are valid Temporal activities and
// already work end-to-end through this wrapper, so they must keep constructing.
func TestActivityAsToolAllowsErrorOnlyReturn(t *testing.T) {
	fn := func(context.Context, validationArgs) error { return nil }
	tl, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{Name: "sideeffect"})
	require.NoError(t, err)
	require.Equal(t, "sideeffect", tl.Name())
}

// TestActivityAsToolAcceptsPointerArgs is the happy path with a pointer TArgs:
// construction succeeds and the schema is still inferred from the pointed-to
// struct.
func TestActivityAsToolAcceptsPointerArgs(t *testing.T) {
	fn := func(context.Context, *validationArgs) (map[string]any, error) { return nil, nil }
	tl, err := googleadk.ActivityAsTool(fn, googleadk.ActivityToolOptions{Name: "pointer-args"})
	require.NoError(t, err)

	d, ok := tl.(declarer)
	require.True(t, ok, "activity tool must expose Declaration()")
	require.NotNil(t, d.Declaration())
	require.NotNil(t, d.Declaration().Parameters)
	require.Contains(t, d.Declaration().Parameters.Properties, "city")
}
