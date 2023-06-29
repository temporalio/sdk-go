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

package protocol_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	protocolpb "go.temporal.io/api/protocol/v1"
	"go.temporal.io/sdk/internal/protocol"
)

type mockInstance struct {
	protocol.Instance
	HandleMessageFunc func(message *protocolpb.Message) error
	HasCompletedFunc  func() bool
}

func (mock *mockInstance) HandleMessage(message *protocolpb.Message) error {
	return mock.HandleMessageFunc(message)
}

func (mock *mockInstance) HasCompleted() bool {
	return mock.HasCompletedFunc()
}

func TestLookup(t *testing.T) {
	type idInstance struct {
		protocol.Instance
		id string
	}
	ctorRunCount := 0
	instID := t.Name()
	ctor := func() protocol.Instance {
		ctorRunCount += 1
		return &idInstance{id: instID}
	}

	reg := protocol.NewRegistry()
	inst := reg.FindOrAdd(instID, ctor)
	require.Equal(t, 1, ctorRunCount)
	require.Equal(t, instID, inst.(*idInstance).id)

	inst2 := reg.FindOrAdd(instID, ctor)
	require.Equal(t, 1, ctorRunCount)
	require.Equal(t, instID, inst2.(*idInstance).id)
}

func TestClearCompleted(t *testing.T) {
	completeProtoCtor := func() protocol.Instance {
		return &mockInstance{
			HasCompletedFunc: func() bool { return true },
		}
	}
	incompleteProtoCtor := func() protocol.Instance {
		return &mockInstance{
			HasCompletedFunc: func() bool { return false },
		}
	}

	reg := protocol.NewRegistry()
	reg.FindOrAdd("complete.1", completeProtoCtor)
	reg.FindOrAdd("complete.2", completeProtoCtor)
	reg.FindOrAdd("incomplete.1", incompleteProtoCtor)
	reg.FindOrAdd("incomplete.2", incompleteProtoCtor)

	reg.ClearCompleted() // should remove complete.1 and complete.2 from registry

	for i, tt := range [...]struct {
		instID        string
		ctorShouldRun bool
	}{
		{instID: "complete.1", ctorShouldRun: true},
		{instID: "complete.2", ctorShouldRun: true},
		{instID: "incomplete.1", ctorShouldRun: false},
		{instID: "incomplete.2", ctorShouldRun: false},
	} {
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			ctorRan := false
			_ = reg.FindOrAdd(tt.instID, func() protocol.Instance {
				ctorRan = true
				return &mockInstance{}
			})
			require.Equal(t, tt.ctorShouldRun, ctorRan)
		})
	}

}
