//go:build linux

package sysinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCGroupInfoUpdateOutsideContainer verifies that Update() gracefully handles
// running outside a cgroup environment by returning (false, nil) instead of an error.
// This exercises the errors.Is(err, fs.ErrNotExist) check in cGroupInfoImpl.Update().
func TestCGroupInfoUpdateOutsideContainer(t *testing.T) {
	info := newCGroupInfo().(*cGroupInfoImpl)
	continueUpdates, err := info.Update()

	// When not in a cgroup (fs.ErrNotExist from cgroup2.Load), Update should
	// return false with no error, signaling to stop trying cgroup updates.
	assert.False(t, continueUpdates, "should return false when cgroup files don't exist")
	assert.NoError(t, err, "should not return error when cgroup files don't exist")
}
