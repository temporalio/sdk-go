package resourcetuner

import (
	"errors"
	"io/fs"
)

// handleCGroupUpdateError interprets the error from a cgroup stats update.
// Returns (false, nil) if cgroup files don't exist (not in a container),
// (true, err) for real errors, and (true, nil) on success.
func handleCGroupUpdateError(err error) (bool, error) {
	if !errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return true, err
	}
	return true, nil
}
