package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCancelerChildInteractions(t *testing.T) {
	t.Parallel()
	// Previously, cancellation propagation was handled using a map, which meant that the order of cancellation was random per run (due to map iteration order randomisation)
	// Switched to a slice to maintain order, so this test ensures that the order is maintained as expected

	// Kinda rough setup, but enough to test the child management logic
	rootCtx := &cancelCtx{done: &channelImpl{}}
	childOne := &cancelCtx{}
	childTwo := &cancelCtx{}
	childThree := &cancelCtx{}

	propagateCancel(rootCtx, childOne)
	assert.Equal(t, rootCtx.children, []canceler{childOne})

	// Should not add the same child twice
	propagateCancel(rootCtx, childOne)
	assert.Equal(t, rootCtx.children, []canceler{childOne})

	propagateCancel(rootCtx, childTwo)
	assert.Equal(t, rootCtx.children, []canceler{childOne, childTwo})

	// Removing a child that hasn't been added should be a no-op
	removeChild(rootCtx, childThree)
	assert.Equal(t, rootCtx.children, []canceler{childOne, childTwo})

	propagateCancel(rootCtx, childThree)
	assert.Equal(t, rootCtx.children, []canceler{childOne, childTwo, childThree})

	// Remove and re-add childTwo to ensure order is maintained
	removeChild(rootCtx, childTwo)
	assert.Equal(t, rootCtx.children, []canceler{childOne, childThree})

	propagateCancel(rootCtx, childTwo)
	assert.Equal(t, rootCtx.children, []canceler{childOne, childThree, childTwo})
}
