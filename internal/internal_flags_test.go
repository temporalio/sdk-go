// The MIT License
//
// Copyright (c) 2023 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
