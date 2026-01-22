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

const testFlag = SDKFlagChildWorkflowErrorExecution

func TestSet(t *testing.T) {
	t.Parallel()

	t.Run("no server sdk metadata support", func(t *testing.T) {
		flags := newSDKFlags(&metadataDisabled)
		flags.set(testFlag)
		require.Empty(t, flags.gatherNewSDKFlags(),
			"flags assigned when servier does not support metadata are dropped")
		require.False(t, flags.tryUse(testFlag, false),
			"flags assigned when servier does not support metadata are dropped")
	})

	t.Run("with server sdk metadata support", func(t *testing.T) {
		flags := newSDKFlags(&metadataEnabled)
		flags.set(testFlag)
		require.Empty(t, flags.gatherNewSDKFlags(),
			"flag set via sdkFlags.set is not 'new'")
		require.True(t, flags.tryUse(testFlag, false),
			"flag set via sdkFlags.set should be immediately visible")
	})
}

func TestTryUse_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		flag         sdkFlag
		currentFlags map[sdkFlag]bool
		record       bool
		wantResult   bool
		wantNewFlag  bool
	}{
		{
			name:         "record=true sets new flag",
			flag:         SDKFlagChildWorkflowErrorExecution,
			currentFlags: map[sdkFlag]bool{},
			record:       true,
			wantResult:   true,
			wantNewFlag:  true,
		},
		{
			name:         "record=false with flag in history returns true",
			flag:         SDKFlagChildWorkflowErrorExecution,
			currentFlags: map[sdkFlag]bool{SDKFlagChildWorkflowErrorExecution: true},
			record:       false,
			wantResult:   true,
			wantNewFlag:  false,
		},
		{
			name:         "record=false without flag in history returns false",
			flag:         SDKFlagChildWorkflowErrorExecution,
			currentFlags: map[sdkFlag]bool{},
			record:       false,
			wantResult:   false,
			wantNewFlag:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := newSDKFlags(&metadataEnabled)
			for f, v := range tt.currentFlags {
				if v {
					flags.set(f)
				}
			}

			result := flags.tryUse(tt.flag, tt.record)

			require.Equal(t, tt.wantResult, result)
			_, isNew := flags.newFlags[tt.flag]
			require.Equal(t, tt.wantNewFlag, isNew)
		})
	}
}

func TestEnabledByDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		flag sdkFlag
		want bool
	}{
		{
			name: "SDKFlagLimitChangeVersionSASize enabled by default",
			flag: SDKFlagLimitChangeVersionSASize,
			want: true,
		},
		{
			name: "unknown flag disabled by default (fail closed)",
			flag: sdkFlag(999),
			want: false,
		},
		{
			name: "SDKFlagUnset disabled by default",
			flag: SDKFlagUnset,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.flag.enabledByDefault())
		})
	}
}

func TestFlagOverrides(t *testing.T) {
	tests := []struct {
		name            string
		envVars         map[string]string
		flag            sdkFlag
		wantEnabled     bool
		wantHasOverride bool
	}{
		{
			name:            "no env vars set",
			envVars:         map[string]string{},
			flag:            SDKFlagBlockedSelectorSignalReceive,
			wantEnabled:     false,
			wantHasOverride: false,
		},
		{
			name:            "bulk enable",
			envVars:         map[string]string{"TEMPORAL_SDK_FLAGS_ENABLE": "5"},
			flag:            SDKFlagBlockedSelectorSignalReceive,
			wantEnabled:     true,
			wantHasOverride: true,
		},
		{
			name: "bulk disable overrides bulk enable",
			envVars: map[string]string{
				"TEMPORAL_SDK_FLAGS_ENABLE":  "5",
				"TEMPORAL_SDK_FLAGS_DISABLE": "5",
			},
			flag:            SDKFlagBlockedSelectorSignalReceive,
			wantEnabled:     false,
			wantHasOverride: true,
		},
		{
			name: "individual flag overrides bulk",
			envVars: map[string]string{
				"TEMPORAL_SDK_FLAGS_DISABLE": "5",
				"TEMPORAL_SDK_FLAG_5":        "true",
			},
			flag:            SDKFlagBlockedSelectorSignalReceive,
			wantEnabled:     true,
			wantHasOverride: true,
		},
		{
			name:            "multiple flags in bulk enable",
			envVars:         map[string]string{"TEMPORAL_SDK_FLAGS_ENABLE": "1,5"},
			flag:            SDKFlagLimitChangeVersionSASize,
			wantEnabled:     true,
			wantHasOverride: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env vars first
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			// Then reset/reload so the new env vars are picked up
			ReloadFlagOverridesFromEnvForTest()

			enabled, hasOverride := getFlagOverride(tt.flag)

			require.Equal(t, tt.wantHasOverride, hasOverride)
			if hasOverride {
				require.Equal(t, tt.wantEnabled, enabled)
			}
		})
	}
}

func TestTryUse_WithEnvAndEnabledByDefault(t *testing.T) {
	tests := []struct {
		name         string
		flag         sdkFlag
		currentFlags map[sdkFlag]bool
		record       bool
		envOverride  *bool
		wantResult   bool
		wantNewFlag  bool
	}{
		{
			name:         "flag in history always returns true",
			flag:         SDKFlagBlockedSelectorSignalReceive,
			currentFlags: map[sdkFlag]bool{SDKFlagBlockedSelectorSignalReceive: true},
			record:       false,
			envOverride:  nil,
			wantResult:   true,
			wantNewFlag:  false,
		},
		{
			name:         "env override enable + record=true enables flag",
			flag:         SDKFlagBlockedSelectorSignalReceive,
			currentFlags: map[sdkFlag]bool{},
			record:       true,
			envOverride:  ptr(true),
			wantResult:   true,
			wantNewFlag:  true,
		},
		{
			name:         "env override enable + record=false returns false",
			flag:         SDKFlagBlockedSelectorSignalReceive,
			currentFlags: map[sdkFlag]bool{},
			record:       false,
			envOverride:  ptr(true),
			wantResult:   false,
			wantNewFlag:  false,
		},
		{
			name:         "env override disable returns false even with record=true",
			flag:         SDKFlagBlockedSelectorSignalReceive,
			currentFlags: map[sdkFlag]bool{},
			record:       true,
			envOverride:  ptr(false),
			wantResult:   false,
			wantNewFlag:  false,
		},
		{
			name:         "enabledByDefault=false without override returns false",
			flag:         SDKFlagBlockedSelectorSignalReceive,
			currentFlags: map[sdkFlag]bool{},
			record:       true,
			envOverride:  nil,
			wantResult:   false,
			wantNewFlag:  false,
		},
		{
			name:         "enabledByDefault=true with record=true enables flag",
			flag:         SDKFlagChildWorkflowErrorExecution,
			currentFlags: map[sdkFlag]bool{},
			record:       true,
			envOverride:  nil,
			wantResult:   true,
			wantNewFlag:  true,
		},
		{
			name:         "enabledByDefault=true with record=false returns false",
			flag:         SDKFlagChildWorkflowErrorExecution,
			currentFlags: map[sdkFlag]bool{},
			record:       false,
			envOverride:  nil,
			wantResult:   false,
			wantNewFlag:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ReloadFlagOverridesFromEnvForTest()

			if tt.envOverride != nil {
				cleanup := SetFlagOverrideForTest(tt.flag, *tt.envOverride)
				defer cleanup()
			}

			flags := newSDKFlags(&metadataEnabled)
			for f, v := range tt.currentFlags {
				if v {
					flags.set(f)
				}
			}

			result := flags.tryUse(tt.flag, tt.record)

			require.Equal(t, tt.wantResult, result)
			_, isNew := flags.newFlags[tt.flag]
			require.Equal(t, tt.wantNewFlag, isNew)
		})
	}
}

func ptr(b bool) *bool { return &b }

func TestSetFlagOverrideForTest(t *testing.T) {
	ReloadFlagOverridesFromEnvForTest()

	// Initially no override
	_, hasOverride := getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.False(t, hasOverride)

	// Set override
	cleanup := SetFlagOverrideForTest(SDKFlagBlockedSelectorSignalReceive, true)
	defer cleanup()

	enabled, hasOverride := getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.True(t, hasOverride)
	require.True(t, enabled)

	// Cleanup restores
	cleanup()

	_, hasOverride = getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.False(t, hasOverride)
}

func TestSetFlagOverrideForTest_OverwriteExisting(t *testing.T) {
	ReloadFlagOverridesFromEnvForTest()

	// Set initial override
	cleanup1 := SetFlagOverrideForTest(SDKFlagBlockedSelectorSignalReceive, true)
	defer cleanup1()

	// Overwrite with different value
	cleanup2 := SetFlagOverrideForTest(SDKFlagBlockedSelectorSignalReceive, false)
	defer cleanup2()

	enabled, _ := getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.False(t, enabled)

	// Inner cleanup restores to first override
	cleanup2()
	enabled, _ = getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.True(t, enabled)

	// Outer cleanup removes entirely
	cleanup1()
	_, hasOverride := getFlagOverride(SDKFlagBlockedSelectorSignalReceive)
	require.False(t, hasOverride)
}

func TestTryUse_MetadataDisabled(t *testing.T) {
	ReloadFlagOverridesFromEnvForTest()
	cleanup := SetFlagOverrideForTest(SDKFlagBlockedSelectorSignalReceive, true)
	defer cleanup()

	flags := newSDKFlags(&metadataDisabled)

	// Even with env override, should return false when metadata disabled
	result := flags.tryUse(SDKFlagBlockedSelectorSignalReceive, true)
	require.False(t, result)
	require.Empty(t, flags.newFlags)
}

func TestBlockedSelectorSignalReceive_Migration(t *testing.T) {
	tests := []struct {
		name          string
		flagInHistory bool
		envOverride   *bool
		wantBehavior  string
	}{
		{
			name:          "new workflow without override uses old behavior",
			flagInHistory: false,
			envOverride:   nil,
			wantBehavior:  "old",
		},
		{
			name:          "new workflow with env override uses new behavior",
			flagInHistory: false,
			envOverride:   ptr(true),
			wantBehavior:  "new",
		},
		{
			name:          "replaying workflow with flag uses new behavior",
			flagInHistory: true,
			envOverride:   nil,
			wantBehavior:  "new",
		},
		{
			name:          "env disable doesn't affect workflow with flag in history",
			flagInHistory: true,
			envOverride:   ptr(false),
			wantBehavior:  "new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ReloadFlagOverridesFromEnvForTest()

			if tt.envOverride != nil {
				cleanup := SetFlagOverrideForTest(SDKFlagBlockedSelectorSignalReceive, *tt.envOverride)
				defer cleanup()
			}

			flags := newSDKFlags(&metadataEnabled)
			if tt.flagInHistory {
				flags.set(SDKFlagBlockedSelectorSignalReceive)
			}

			// Simulate what internal_workflow.go does
			isReplay := tt.flagInHistory
			dropSignalFlag := flags.tryUse(SDKFlagBlockedSelectorSignalReceive, !isReplay)

			if tt.wantBehavior == "new" {
				require.True(t, dropSignalFlag)
			} else {
				require.False(t, dropSignalFlag)
			}
		})
	}
}
