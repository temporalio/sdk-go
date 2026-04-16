package worker_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func TestNew_NegativeMaxConcurrentWorkflowTaskExternalStorageVisits(t *testing.T) {
	c, err := client.NewLazyClient(client.Options{})
	require.NoError(t, err)
	require.PanicsWithValue(t, "MaxConcurrentWorkflowTaskExternalStorageVisits must not be negative", func() {
		worker.New(c, "my-task-queue", worker.Options{
			MaxConcurrentWorkflowTaskExternalStorageVisits: -1,
		})
	})
}
