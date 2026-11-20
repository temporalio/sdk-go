package internal

import (
	"context"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservicemock/v1"

	ilog "go.temporal.io/sdk/internal/log"
)

// Separate types model same-named functions from different packages.
type aaV1 struct{}
type aaV2 struct{}

func (*aaV1) aaActivity(context.Context) error { return nil }
func (*aaV2) aaActivity(context.Context) error { return nil }
func (*aaV1) aaWorkflow(Context) error         { return nil }
func (*aaV2) aaWorkflow(Context) error         { return nil }

func aaOtherActivity(context.Context) error { return nil }
func aaOtherWorkflow(Context) error         { return nil }

func AaDoWork(context.Context) error         { return nil }
func (*aaV2) AaDoWork(context.Context) error { return nil }

func TestActivityNameCollisionWarning(t *testing.T) {
	v1, v2 := &aaV1{}, &aaV2{}
	tests := []struct {
		name     string
		register func(*AggregatedWorker)
	}{
		{
			name: "same Go name with a default name and a custom name",
			register: func(w *AggregatedWorker) {
				w.RegisterActivity(v1.aaActivity)
				w.RegisterActivityWithOptions(v2.aaActivity, RegisterActivityOptions{Name: "aaActivity_v2"})
			},
		},
		{
			name: "same Go name with a custom name and a default name",
			register: func(w *AggregatedWorker) {
				w.RegisterActivityWithOptions(v1.aaActivity, RegisterActivityOptions{Name: "aaActivity_v1"})
				w.RegisterActivity(v2.aaActivity)
			},
		},
		{
			name: "same Go name with two custom names",
			register: func(w *AggregatedWorker) {
				w.RegisterActivityWithOptions(v1.aaActivity, RegisterActivityOptions{Name: "aaActivity_v1"})
				w.RegisterActivityWithOptions(v2.aaActivity, RegisterActivityOptions{Name: "aaActivity_v2"})
			},
		},
		{
			name: "registered name matches another function's Go name",
			register: func(w *AggregatedWorker) {
				w.RegisterActivityWithOptions(v1.aaActivity, RegisterActivityOptions{Name: "aaOtherActivity"})
				w.RegisterActivityWithOptions(aaOtherActivity, RegisterActivityOptions{Name: "aaOtherActivity_v2"})
			},
		},
		{
			name: "standalone function and a prefixed struct method",
			register: func(w *AggregatedWorker) {
				w.RegisterActivityWithOptions(AaDoWork, RegisterActivityOptions{Name: "aaDoWork"})
				w.RegisterActivityWithOptions(v2, RegisterActivityOptions{Name: "aaPrefix_"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			client := NewServiceClient(service, nil, ClientOptions{Logger: logger})
			w := NewAggregatedWorker(client, "aaTaskQueue", WorkerOptions{})

			tt.register(w)

			require.Regexp(t, `WARN\s+Found two activities called`, strings.Join(logger.Lines(), ""))
		})
	}
}

func TestWorkflowNameCollisionWarning(t *testing.T) {
	v1, v2 := &aaV1{}, &aaV2{}
	tests := []struct {
		name     string
		register func(*AggregatedWorker)
	}{
		{
			name: "same Go name with a default name and a custom name",
			register: func(w *AggregatedWorker) {
				w.RegisterWorkflow(v1.aaWorkflow)
				w.RegisterWorkflowWithOptions(v2.aaWorkflow, RegisterWorkflowOptions{Name: "aaWorkflow_v2"})
			},
		},
		{
			name: "same Go name with a custom name and a default name",
			register: func(w *AggregatedWorker) {
				w.RegisterWorkflowWithOptions(v1.aaWorkflow, RegisterWorkflowOptions{Name: "aaWorkflow_v1"})
				w.RegisterWorkflow(v2.aaWorkflow)
			},
		},
		{
			name: "same Go name with two custom names",
			register: func(w *AggregatedWorker) {
				w.RegisterWorkflowWithOptions(v1.aaWorkflow, RegisterWorkflowOptions{Name: "aaWorkflow_v1"})
				w.RegisterWorkflowWithOptions(v2.aaWorkflow, RegisterWorkflowOptions{Name: "aaWorkflow_v2"})
			},
		},
		{
			name: "registered name matches another function's Go name",
			register: func(w *AggregatedWorker) {
				w.RegisterWorkflowWithOptions(v1.aaWorkflow, RegisterWorkflowOptions{Name: "aaOtherWorkflow"})
				w.RegisterWorkflowWithOptions(aaOtherWorkflow, RegisterWorkflowOptions{Name: "aaOtherWorkflow_v2"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			client := NewServiceClient(service, nil, ClientOptions{Logger: logger})
			w := NewAggregatedWorker(client, "aaTaskQueue", WorkerOptions{})

			tt.register(w)

			require.Regexp(t, `WARN\s+Found two workflows called`, strings.Join(logger.Lines(), ""))
		})
	}
}
