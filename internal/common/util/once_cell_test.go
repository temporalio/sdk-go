package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_PopulatedOnceCell(t *testing.T) {
	oc := PopulatedOnceCell("hi")
	assert.Equal(t, "hi", oc.Get())
}

func Test_LazyOnceCell(t *testing.T) {
	oc := LazyOnceCell(func() string { return "hi" })
	assert.Equal(t, "hi", oc.Get())
}
