package statemachines

import (
	"testing"
)

func TestDecodeArg(t *testing.T) {
	t.Parallel()

	afsm := NewActivityStateMachine()
	print(afsm.definition.Visualize())
}
