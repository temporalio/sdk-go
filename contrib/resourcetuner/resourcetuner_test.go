// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package resourcetuner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type FakeSystemInfoSupplier struct {
	memUse float64
	cpuUse float64
}

func (f FakeSystemInfoSupplier) GetMemoryUsage() (float64, error) {
	return f.memUse, nil
}

func (f FakeSystemInfoSupplier) GetCpuUsage() (float64, error) {
	return f.cpuUse, nil
}

func TestPidDecisions(t *testing.T) {
	fakeSupplier := &FakeSystemInfoSupplier{memUse: 0.5, cpuUse: 0.5}
	rcOpts := DefaultResourceControllerOptions()
	rcOpts.memTargetPercent = 0.8
	rcOpts.cpuTargetPercent = 0.9
	rc := newResourceController(rcOpts, fakeSupplier)

	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision()
		assert.NoError(t, err)
		assert.True(t, decision)

		assert.InDelta(t, 1.5, rc.memPid.State.ControlSignal, 0.001)
		assert.InDelta(t, 2.0, rc.cpuPid.State.ControlSignal, 0.001)
	}

	fakeSupplier.memUse = 0.8
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision()
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.9
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision()
		assert.NoError(t, err)
		assert.False(t, decision)
	}

	fakeSupplier.memUse = 0.7
	fakeSupplier.cpuUse = 0.7
	for i := 0; i < 10; i++ {
		decision, err := rc.pidDecision()
		assert.NoError(t, err)
		assert.True(t, decision)
	}
}
