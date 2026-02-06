//go:build linux

package resourcetuner

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// TestCGroupInfoUpdateOutsideContainer verifies that Update() gracefully handles
// running outside a cgroup environment by returning (false, nil) instead of an error.
// This exercises the errors.Is(err, fs.ErrNotExist) check in cGroupInfoImpl.Update().
func TestCGroupInfoUpdateOutsideContainer(t *testing.T) {
	info := newCGroupInfo().(*cGroupInfoImpl)
	continueUpdates, err := info.Update()

	_, err := cgroup2.Load("/")

	if errors.Is(err, fs.ErrNotExist) {
		assert.False(t, continueUpdates, "should return false when cgroup files don't exist")
		assert.NoError(t, err, "should not return error when cgroup files don't exist")
	}

}
