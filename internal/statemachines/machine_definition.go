// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
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

package statemachines

import (
	"bytes"
	"fmt"
	"sort"
)

type State interface {
	// Must return a uniquely identifying string for the state
	Name() string
}

// TODO: Dynamic transitions don't quite work here?
type Transition struct {
	from  State
	event MachineEvent
}

type TransitionAction struct {
	dest State
}

type TransitionWithAction struct {
	transition Transition
	action     TransitionAction
}

type StateMachineDefinition struct {
	name         string
	initialState State
	finalStates  []State
	// TODO: Doubt we really need these. Looks like it was just used as "invalid transition" in java but the transition
	//  map is sufficient for that.
	//validHistoryEvents  map[enumspb.EventType]bool
	//validExplicitEvents map[ExplicitEvent]bool
	transitions map[Transition]TransitionAction
}

func BuildStateMachine(name string, initialState State, finalStates []State) StateMachineDefinition {
	return StateMachineDefinition{
		name:         name,
		initialState: initialState,
		finalStates:  finalStates,
		transitions:  make(map[Transition]TransitionAction),
	}
}

func (d *StateMachineDefinition) add(from State, event MachineEvent, to State) {
	trans := Transition{event: event, from: from}

	_, isPresent := d.transitions[trans]
	if isPresent {
		panic("Transition already exists in machine definition!")
	}

	d.transitions[trans] = TransitionAction{dest: to}
}

func (d *StateMachineDefinition) sortedTransitions() []TransitionWithAction {
	keys := make([]TransitionWithAction, len(d.transitions))
	i := 0
	for k, v := range d.transitions {
		keys[i] = TransitionWithAction{k, v}
		i++
	}
	sort.Slice(keys, func(a, b int) bool {
		return keys[a].transition.event.name() < keys[b].transition.event.name()
	})

	return keys
}

// Visualize outputs a visualization of the state machine in GraphViz format
func (d *StateMachineDefinition) Visualize() string {
	var buf bytes.Buffer

	sortedTransitions := d.sortedTransitions()
	allStateNames := make(map[string]bool)
	for _, trans := range sortedTransitions {
		allStateNames[trans.transition.from.Name()] = true
		allStateNames[trans.action.dest.Name()] = true
	}

	writeHeaderLine(&buf)
	writeTransitions(&buf, d.initialState.Name(), sortedTransitions)
	writeStates(&buf, allStateNames)
	writeFooter(&buf)

	return buf.String()
}

func writeHeaderLine(buf *bytes.Buffer) {
	buf.WriteString(fmt.Sprintf(`digraph fsm {`))
	buf.WriteString("\n")
}

func writeTransitions(buf *bytes.Buffer, current string, sortedEKeys []TransitionWithAction) {
	// make sure the current state is at top
	for _, k := range sortedEKeys {
		if k.transition.from.Name() == current {
			buf.WriteString(fmt.Sprintf(`    "%s" -> "%s" [ label = "%s" ];`,
				k.transition.from.Name(), k.action.dest.Name(), k.transition.event.name()))
			buf.WriteString("\n")
		}
	}
	for _, k := range sortedEKeys {
		if k.transition.from.Name() != current {
			buf.WriteString(fmt.Sprintf(`    "%s" -> "%s" [ label = "%s" ];`,
				k.transition.from.Name(), k.action.dest.Name(), k.transition.event.name()))
			buf.WriteString("\n")
		}
	}

	buf.WriteString("\n")
}

func writeStates(buf *bytes.Buffer, allStateNames map[string]bool) {
	for k := range allStateNames {
		buf.WriteString(fmt.Sprintf(`    "%s";`, k))
		buf.WriteString("\n")
	}
}

func writeFooter(buf *bytes.Buffer) {
	buf.WriteString(fmt.Sprintln("}"))
}
