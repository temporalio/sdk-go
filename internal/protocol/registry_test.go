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
