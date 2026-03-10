package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandleCGroupUpdateError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantContinue    bool
		wantErrContains string
	}{
		{
			name:         "not in cgroup - fs.ErrNotExist stops updates without error",
			err:          fs.ErrNotExist,
			wantContinue: false,
		},
		{
			name:         "wrapped fs.ErrNotExist also stops updates without error",
			err:          fmt.Errorf("failed to get cgroup mem stats %w", fs.ErrNotExist),
			wantContinue: false,
		},
		{
			name:         "success continues updates",
			err:          nil,
			wantContinue: true,
		},
		{
			name:            "other errors continue updates and propagate",
			err:             errors.New("something broke"),
			wantContinue:    true,
			wantErrContains: "something broke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			continueUpdates, err := handleCGroupUpdateError(tt.err)
			assert.Equal(t, tt.wantContinue, continueUpdates)
			if tt.wantErrContains != "" {
				assert.ErrorContains(t, err, tt.wantErrContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
