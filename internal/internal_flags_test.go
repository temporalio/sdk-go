package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
)

var (
	metadataDisabled = workflowservice.GetSystemInfoResponse_Capabilities{}
	metadataEnabled  = workflowservice.GetSystemInfoResponse_Capabilities{
		SdkMetadata: true,
	}
)

func TestLoadFlagOverridesFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		wantVal bool
		wantSet bool
	}{
		{"1 enables", "1", true, true},
		{"0 disables", "0", false, true},
		{"invalid value ignored", "true", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEMPORAL_SDK_FLAG_5", tt.envVal)
			overrides := make(map[sdkFlag]bool)
			loadFlagOverridesFromEnv(overrides)

			val, ok := overrides[SDKFlagBlockedSelectorSignalReceive]
			require.Equal(t, tt.wantSet, ok)
			if ok {
				require.Equal(t, tt.wantVal, val)
			}
		})
	}
}

func TestSet(t *testing.T) {
	t.Run("metadata disabled drops flags", func(t *testing.T) {
		flags := newSDKFlagSet(&metadataDisabled)
		flags.set(SDKFlagChildWorkflowErrorExecution)
		require.False(t, flags.currentFlags[SDKFlagChildWorkflowErrorExecution])
	})

	t.Run("metadata enabled keeps flags", func(t *testing.T) {
		flags := newSDKFlagSet(&metadataEnabled)
		flags.set(SDKFlagChildWorkflowErrorExecution)
		require.True(t, flags.currentFlags[SDKFlagChildWorkflowErrorExecution])
		require.Empty(t, flags.gatherNewSDKFlags(), "set() flags are not 'new'")
	})
}

func TestTryUse(t *testing.T) {
	tests := []struct {
		name        string
		inHistory   bool
		record      bool
		flagDefault bool
		wantResult  bool
		wantNewFlag bool
	}{
		{
			name:        "in history returns true regardless of default or record",
			inHistory:   true,
			record:      false,
			flagDefault: false,
			wantResult:  true,
			wantNewFlag: false,
		},
		{
			name:        "default=true record=true enables and records flag",
			inHistory:   false,
			record:      true,
			flagDefault: true,
			wantResult:  true,
			wantNewFlag: true,
		},
		{
			name:        "default=true record=false returns false",
			inHistory:   false,
			record:      false,
			flagDefault: true,
			wantResult:  false,
			wantNewFlag: false,
		},
		{
			name:        "default=false record=true returns false",
			inHistory:   false,
			record:      true,
			flagDefault: false,
			wantResult:  false,
			wantNewFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := sdkFlagsAllowed[SDKFlagBlockedSelectorSignalReceive]
			defer func() { sdkFlagsAllowed[SDKFlagBlockedSelectorSignalReceive] = orig }()

			sdkFlagsAllowed[SDKFlagBlockedSelectorSignalReceive] = tt.flagDefault

			flags := newSDKFlagSet(&metadataEnabled)
			if tt.inHistory {
				flags.set(SDKFlagBlockedSelectorSignalReceive)
			}

			result := flags.tryUse(SDKFlagBlockedSelectorSignalReceive, tt.record)

			require.Equal(t, tt.wantResult, result)
			_, isNew := flags.newFlags[SDKFlagBlockedSelectorSignalReceive]
			require.Equal(t, tt.wantNewFlag, isNew)
		})
	}
}
