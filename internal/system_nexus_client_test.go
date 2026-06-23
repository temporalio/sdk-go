package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewNexusClientAllowsSystemPair(t *testing.T) {
	// The built-in system Nexus endpoint/service pair is permitted despite the
	// reserved __temporal_ prefix.
	require.NotPanics(t, func() {
		c := NewNexusClient(systemNexusEndpoint, systemNexusService)
		require.Equal(t, systemNexusEndpoint, c.Endpoint())
		require.Equal(t, systemNexusService, c.Service())
	})
}

func TestNewNexusClientStillRejectsOtherReservedPrefixes(t *testing.T) {
	// A reserved-prefix endpoint that is not the system pair still panics.
	require.Panics(t, func() {
		NewNexusClient("__temporal_other", "my.Service")
	})
	// A reserved-prefix service that is not the system pair still panics.
	require.Panics(t, func() {
		NewNexusClient("my-endpoint", "__temporal_other")
	})
	// The system endpoint with a non-system service is not exempt.
	require.Panics(t, func() {
		NewNexusClient(systemNexusEndpoint, "__temporal_other")
	})
}
