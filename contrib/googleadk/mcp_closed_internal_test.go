package googleadk

// White-box tests for the terminal behavior of Activities.Close: mcpToolset and
// mcpClosed are unexported. The externally-visible lifecycle behavior (caching,
// Close-closes-toolsets, idempotency) is covered in mcp_lifecycle_test.go.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMcpToolsetClosedIsTerminal proves Close is terminal: a late or
// Temporal-retried MCP Activity resolving a toolset after Close fails
// non-retryably instead of silently repopulating the cache with a toolset
// nothing will ever close.
func TestMcpToolsetClosedIsTerminal(t *testing.T) {
	server := NewFakeMCPServer("late-server").AddTool("noop", "noop tool", nil,
		func(map[string]any) (map[string]any, error) { return map[string]any{}, nil })
	acts, err := NewActivities(Config{
		MCPToolsets: map[string]MCPFactory{"late-server": server.Factory()},
	})
	require.NoError(t, err)

	// Sanity: resolvable (and cached) before Close.
	_, err = acts.mcpToolset(context.Background(), "late-server")
	require.NoError(t, err)

	require.NoError(t, acts.Close())

	_, err = acts.mcpToolset(context.Background(), "late-server")
	require.Error(t, err)
	assert.True(t, IsNonRetryable(err), "post-Close resolution must not burn the Activity retry budget")
	assert.Contains(t, err.Error(), "closed")
	assert.Empty(t, acts.mcpCache, "post-Close resolution must not repopulate the cache")
}
