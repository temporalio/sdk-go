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
