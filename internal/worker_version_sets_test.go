package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func Test_WorkerVersionSets_fromProtoResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *workflowservice.GetWorkerBuildIdCompatibilityResponse
		want     *WorkerBuildIDVersionSets
	}{
		{
			name:     "nil response",
			response: nil,
			want:     nil,
		},
		{
			name: "normal sets",
			response: &workflowservice.GetWorkerBuildIdCompatibilityResponse{
				MajorVersionSets: []*taskqueuepb.CompatibleVersionSet{
					{BuildIds: []string{"1.0", "1.1"}},
					{BuildIds: []string{"2.0"}},
				},
			},
			want: &WorkerBuildIDVersionSets{
				Sets: []*CompatibleVersionSet{
					{BuildIDs: []string{"1.0", "1.1"}},
					{BuildIDs: []string{"2.0"}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, workerVersionSetsFromProtoResponse(tt.response), "workerVersionSetsFromProtoResponse(%v)", tt.response)
		})
	}
}

func Test_VersioningIntentOld(t *testing.T) {
	tests := []struct {
		name                string
		intent              VersioningIntent
		tqSame              bool
		shouldUseCompatible bool
	}{
		{
			name:                "Unspecified same TQ",
			intent:              VersioningIntentUnspecified,
			tqSame:              true,
			shouldUseCompatible: true,
		},
		{
			name:                "Unspecified different TQ",
			intent:              VersioningIntentUnspecified,
			tqSame:              false,
			shouldUseCompatible: false,
		},
		{
			name:                "Default same TQ",
			intent:              VersioningIntentDefault,
			tqSame:              true,
			shouldUseCompatible: false,
		},
		{
			name:                "Default different TQ",
			intent:              VersioningIntentDefault,
			tqSame:              false,
			shouldUseCompatible: false,
		},
		{
			name:                "Compatible same TQ",
			intent:              VersioningIntentCompatible,
			tqSame:              true,
			shouldUseCompatible: true,
		},
		{
			name:                "Compatible different TQ",
			intent:              VersioningIntentCompatible,
			tqSame:              false,
			shouldUseCompatible: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tqA := "a"
			tqB := "b"
			if tt.tqSame {
				tqB = tqA
			}
			assert.Equal(t,
				tt.shouldUseCompatible, determineInheritBuildIdFlagForCommand(tt.intent, tqA, tqB))
		})
	}
}

func Test_VersioningIntent_EmptyTargetTQ(t *testing.T) {
	assert.Equal(t, true, determineInheritBuildIdFlagForCommand(VersioningIntentUnspecified, "something", ""))
}
