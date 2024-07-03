// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package testsuite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/internal/log"
)

func TestWaitServerReady_respectsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	hostPort, err := getFreeHostPort()
	require.NoError(t, err, "get free host port")

	startTime := time.Now()
	_, err = waitServerReady(ctx, client.Options{
		HostPort:  hostPort,
		Namespace: "default",
		Logger:    log.NewNopLogger(),
	})
	require.Error(t, err, "Dial should fail")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.WithinDuration(t,
		startTime.Add(time.Millisecond),
		time.Now(),
		5*time.Millisecond,
		// Even though the timeout is only a millisecond,
		// we'll allow for a slack of up to 5 milliseconds
		// to account for slow CI machines.
		// Anything smaller than 1 second is fine to use here.
	)
}
