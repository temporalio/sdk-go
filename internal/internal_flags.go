package internal

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"go.temporal.io/api/workflowservice/v1"
)

// sdkFlag represents a flag used to help version the sdk internally to make breaking changes
// in workflow logic.
type sdkFlag uint32

const (
	SDKFlagUnset sdkFlag = 0
	// LimitChangeVersionSASize will limit the search attribute size of TemporalChangeVersion to 2048 when
	// calling GetVersion. If the limit is exceeded the search attribute is not updated.
	SDKFlagLimitChangeVersionSASize = 1
	// SDKFlagChildWorkflowErrorExecution return errors to child workflow execution future if the child workflow would
	// fail in the synchronous path.
	SDKFlagChildWorkflowErrorExecution = 2
	// SDKFlagProtocolMessageCommand uses ProtocolMessageCommands inserted into
	// a workflow task response's command set to order messages with respect to
	// commands.
	SDKFlagProtocolMessageCommand = 3
	// SDKPriorityUpdateHandling will cause update request to be handled before the main workflow method.
	// It will also cause the SDK to immediately handle updates when a handler is registered.
	SDKPriorityUpdateHandling = 4
	// SDKFlagBlockedSelectorSignalReceive will cause a signal to not be lost
	// when the Default path is blocked.
	SDKFlagBlockedSelectorSignalReceive = 5
	SDKFlagUnknown                      = math.MaxUint32
)

// enabledByDefault returns whether this flag should be enabled by default for new
// workflows. Flags must be explicitly listed to be enabled - unlisted flags default
// to false. This ensures new flags don't accidentally ship enabled.
//
// We need to wait at least one release between a new SDK flag being introduced
// and when it's enabled, for to backwards compatibility
func (f sdkFlag) enabledByDefault() bool {
	switch f {
	case SDKFlagLimitChangeVersionSASize,
		SDKFlagChildWorkflowErrorExecution,
		SDKFlagProtocolMessageCommand,
		SDKPriorityUpdateHandling:
		return true
	case SDKFlagBlockedSelectorSignalReceive:
		return false
	default:
		return false
	}
}

// sdkFlagOverrides holds env var overrides for SDK flags.
var sdkFlagOverrides map[sdkFlag]bool

func init() {
	loadFlagOverrides()
}

// Env var format: TEMPORAL_SDK_FLAG_<FLAG_ID>=true|false
// Example: TEMPORAL_SDK_FLAG_5=true (enables SDKFlagBlockedSelectorSignalReceive)
//
// Bulk override: TEMPORAL_SDK_FLAGS_ENABLE=1,5,6 or TEMPORAL_SDK_FLAGS_DISABLE=1,5
//
// NOTE: Using env vars to set flags is strongly discouraged, but this utility is built in
// as an emergency mechanism in case there is an unanticipated bug with a flag flip, where
// users would not have to wait until the next release to upgrade.
func loadFlagOverrides() {
	sdkFlagOverrides = make(map[sdkFlag]bool)

	// Check bulk enable
	if enableList := os.Getenv("TEMPORAL_SDK_FLAGS_ENABLE"); enableList != "" {
		for _, idStr := range strings.Split(enableList, ",") {
			if id, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 32); err == nil {
				flag := sdkFlagFromUint(uint32(id))
				if flag.isValid() {
					sdkFlagOverrides[flag] = true
				}
			}
		}
	}

	// Check bulk disable (takes precedence if both set)
	if disableList := os.Getenv("TEMPORAL_SDK_FLAGS_DISABLE"); disableList != "" {
		for _, idStr := range strings.Split(disableList, ",") {
			if id, err := strconv.ParseUint(strings.TrimSpace(idStr), 10, 32); err == nil {
				flag := sdkFlagFromUint(uint32(id))
				if flag.isValid() {
					sdkFlagOverrides[flag] = false
				}
			}
		}
	}

	// Individual flag overrides (highest precedence)
	// Iterate up to a reasonable bound
	for i := uint32(1); i <= 100; i++ {
		flag := sdkFlagFromUint(i)
		if !flag.isValid() {
			continue
		}
		envKey := fmt.Sprintf("TEMPORAL_SDK_FLAG_%d", i)
		if val := os.Getenv(envKey); val != "" {
			if parsed, err := strconv.ParseBool(val); err == nil {
				sdkFlagOverrides[flag] = parsed
			}
		}
	}
}

func getFlagOverride(flag sdkFlag) (enabled bool, hasOverride bool) {
	enabled, hasOverride = sdkFlagOverrides[flag]
	return
}

func sdkFlagFromUint(value uint32) sdkFlag {
	switch value {
	case uint32(SDKFlagUnset):
		return SDKFlagUnset
	case uint32(SDKFlagLimitChangeVersionSASize):
		return SDKFlagLimitChangeVersionSASize
	case uint32(SDKFlagChildWorkflowErrorExecution):
		return SDKFlagChildWorkflowErrorExecution
	case uint32(SDKFlagProtocolMessageCommand):
		return SDKFlagProtocolMessageCommand
	case uint32(SDKPriorityUpdateHandling):
		return SDKPriorityUpdateHandling
	case uint32(SDKFlagBlockedSelectorSignalReceive):
		return SDKFlagBlockedSelectorSignalReceive
	default:
		return SDKFlagUnknown
	}
}

func (f sdkFlag) isValid() bool {
	return f != SDKFlagUnset && f != SDKFlagUnknown
}

// sdkFlags represents all the flags that are currently set in a workflow execution.
type sdkFlags struct {
	capabilities *workflowservice.GetSystemInfoResponse_Capabilities
	// Flags that have been received from the server
	currentFlags map[sdkFlag]bool
	// Flags that have been set this WFT that have not been sent to the server.
	// Keep track of them separately so we know what to send to the server.
	newFlags map[sdkFlag]bool
}

func newSDKFlags(capabilities *workflowservice.GetSystemInfoResponse_Capabilities) *sdkFlags {
	return &sdkFlags{
		capabilities: capabilities,
		currentFlags: make(map[sdkFlag]bool),
		newFlags:     make(map[sdkFlag]bool),
	}
}

// tryUse checks if a flag should be used, respecting enabledByDefault config and env var overrides.
func (sf *sdkFlags) tryUse(flag sdkFlag, record bool) bool {
	if !sf.capabilities.GetSdkMetadata() {
		return false
	}

	// Flag already in history - return true
	if sf.currentFlags[flag] || sf.newFlags[flag] {
		return true
	}

	if !record {
		return false
	}

	// Env var override takes priority
	if enabled, hasOverride := getFlagOverride(flag); hasOverride {
		if enabled {
			sf.newFlags[flag] = true
		}
		return enabled
	}

	if !flag.enabledByDefault() {
		return false
	}

	sf.newFlags[flag] = true
	return true
}

// getFlag returns true if the flag is currently set.
func (sf *sdkFlags) getFlag(flag sdkFlag) bool {
	return sf.currentFlags[flag] || sf.newFlags[flag]
}

// set marks a flag as in current use regardless of replay status.
func (sf *sdkFlags) set(flags ...sdkFlag) {
	if !sf.capabilities.GetSdkMetadata() {
		return
	}
	for _, flag := range flags {
		sf.currentFlags[flag] = true
	}
}

// markSDKFlagsSent marks all sdk flags as sent to the server.
func (sf *sdkFlags) markSDKFlagsSent() {
	for flag := range sf.newFlags {
		sf.currentFlags[flag] = true
	}
	sf.newFlags = make(map[sdkFlag]bool)
}

// gatherNewSDKFlags returns all sdkFlags set since the last call to markSDKFlagsSent.
func (sf *sdkFlags) gatherNewSDKFlags() []sdkFlag {
	flags := make([]sdkFlag, 0, len(sf.newFlags))
	for flag := range sf.newFlags {
		flags = append(flags, flag)
	}
	return flags
}

// SetFlagOverrideForTest sets a flag override for testing. Returns a cleanup function.
// For test use only.
func SetFlagOverrideForTest(flag sdkFlag, enabled bool) func() {
	old, existed := sdkFlagOverrides[flag]
	sdkFlagOverrides[flag] = enabled
	return func() {
		if existed {
			sdkFlagOverrides[flag] = old
		} else {
			delete(sdkFlagOverrides, flag)
		}
	}
}

// ReloadFlagOverridesFromEnvForTest reloads flag overrides from environment variables.
// This clears any overrides set via SetFlagOverrideForTest.
// For test use only.
func ReloadFlagOverridesFromEnvForTest() {
	loadFlagOverrides()
}
