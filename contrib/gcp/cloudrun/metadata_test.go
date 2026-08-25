package cloudrun_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/gcp/cloudrun"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// testInstanceID is a representative Cloud Run instance ID as returned by the GCP metadata server.
const testInstanceID = "3855948589192"

// newStubMetadataServer starts an in-process HTTP server that imitates the GCP metadata server,
// responding to every request with the given status code and body.
func newStubMetadataServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

// TestFetchMetadata_EnvPrecedence covers spec item 1: the worker pool variables win over the
// service variables, and the name and revision are resolved independently. Every case sets all four
// variables (empty string means "unset") so the host environment cannot influence the result.
func TestFetchMetadata_EnvPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantName string
		wantRev  string
	}{
		{
			name: "worker pool vars win over service vars",
			env: map[string]string{
				"CLOUD_RUN_WORKER_POOL": "my-pool",
				"CLOUD_RUN_REVISION":    "my-pool-00007-abc",
				"K_SERVICE":             "my-service",
				"K_REVISION":            "my-service-00003-xyz",
			},
			wantName: "my-pool",
			wantRev:  "my-pool-00007-abc",
		},
		{
			name: "falls back to service vars when worker pool vars absent",
			env: map[string]string{
				"CLOUD_RUN_WORKER_POOL": "",
				"CLOUD_RUN_REVISION":    "",
				"K_SERVICE":             "my-service",
				"K_REVISION":            "my-service-00003-xyz",
			},
			wantName: "my-service",
			wantRev:  "my-service-00003-xyz",
		},
		{
			name: "name and revision are resolved independently",
			env: map[string]string{
				"CLOUD_RUN_WORKER_POOL": "my-pool",
				"CLOUD_RUN_REVISION":    "",
				"K_SERVICE":             "my-service",
				"K_REVISION":            "my-service-00003-xyz",
			},
			wantName: "my-pool",              // CLOUD_RUN_WORKER_POOL wins for the name
			wantRev:  "my-service-00003-xyz", // K_REVISION is used because CLOUD_RUN_REVISION is empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			srv := newStubMetadataServer(http.StatusOK, testInstanceID)
			defer srv.Close()

			md, err := cloudrun.FetchMetadata(context.Background(), cloudrun.WithMetadataURL(srv.URL))
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, md.Name)
			assert.Equal(t, tt.wantRev, md.Revision)
			assert.Equal(t, testInstanceID, md.InstanceID)
		})
	}
}

// TestFetchMetadata_SendsHeaderAndTrimsBody covers spec item 4: the request carries the
// Metadata-Flavor: Google header and the instance ID read from the body is trimmed.
func TestFetchMetadata_SendsHeaderAndTrimsBody(t *testing.T) {
	// A buffered channel records the header from the server goroutine race-free.
	gotFlavor := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFlavor <- r.Header.Get("Metadata-Flavor")
		// Surround the ID with whitespace to prove FetchMetadata trims it.
		_, _ = io.WriteString(w, "  "+testInstanceID+"\n")
	}))
	defer srv.Close()

	md, err := cloudrun.FetchMetadata(context.Background(), cloudrun.WithMetadataURL(srv.URL))
	require.NoError(t, err)
	assert.Equal(t, "Google", <-gotFlavor)
	assert.Equal(t, testInstanceID, md.InstanceID)
}

// TestFetchMetadata_ErrorOnNon200 covers spec item 4: a non-200 response is a clear error and the
// status code is surfaced.
func TestFetchMetadata_ErrorOnNon200(t *testing.T) {
	srv := newStubMetadataServer(http.StatusInternalServerError, "boom")
	defer srv.Close()

	md, err := cloudrun.FetchMetadata(context.Background(), cloudrun.WithMetadataURL(srv.URL))
	require.Error(t, err)
	assert.Nil(t, md)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "cloudrun:")
}

// TestFetchMetadata_ErrorWhenUnreachable covers spec item 4: an unreachable metadata server is a
// clear error. The custom client (via WithHTTPClient) bounds the failed dial.
func TestFetchMetadata_ErrorWhenUnreachable(t *testing.T) {
	srv := newStubMetadataServer(http.StatusOK, testInstanceID)
	url := srv.URL
	srv.Close() // Nothing is listening on url now.

	md, err := cloudrun.FetchMetadata(
		context.Background(),
		cloudrun.WithMetadataURL(url),
		cloudrun.WithHTTPClient(&http.Client{Timeout: time.Second}),
	)
	require.Error(t, err)
	assert.Nil(t, md)
	assert.Contains(t, err.Error(), "cloudrun:")
}

// TestMetadata_WorkerIdentity covers spec item 2: the identity is instanceID@revision, falling back
// to instanceID@name and then to a bare instanceID.
func TestMetadata_WorkerIdentity(t *testing.T) {
	tests := []struct {
		name string
		md   cloudrun.Metadata
		want string
	}{
		{
			name: "instanceID@revision, revision preferred over name",
			md:   cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool", Revision: "rev-1"},
			want: "i-1@rev-1",
		},
		{
			name: "falls back to instanceID@name when revision empty",
			md:   cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool"},
			want: "i-1@my-pool",
		},
		{
			name: "bare instanceID when name and revision empty",
			md:   cloudrun.Metadata{InstanceID: "i-1"},
			want: "i-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.md.WorkerIdentity())
		})
	}
}

// TestMetadata_DeploymentVersion covers spec item 3: the version is (deploymentName=name,
// buildID=revision), and it is a clear error when either the name or the revision is empty.
func TestMetadata_DeploymentVersion(t *testing.T) {
	t.Run("name and revision set", func(t *testing.T) {
		md := cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool", Revision: "rev-1"}
		v, err := md.DeploymentVersion()
		require.NoError(t, err)
		assert.Equal(t, worker.WorkerDeploymentVersion{DeploymentName: "my-pool", BuildID: "rev-1"}, v)
	})

	errorCases := []struct {
		name string
		md   cloudrun.Metadata
	}{
		{"name empty", cloudrun.Metadata{InstanceID: "i-1", Revision: "rev-1"}},
		{"revision empty", cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool"}},
		{"both empty", cloudrun.Metadata{InstanceID: "i-1"}},
	}
	for _, tt := range errorCases {
		t.Run("error when "+tt.name, func(t *testing.T) {
			v, err := tt.md.DeploymentVersion()
			require.Error(t, err)
			assert.Equal(t, worker.WorkerDeploymentVersion{}, v)
			assert.Contains(t, err.Error(), "cloudrun:")
		})
	}
}

// TestMetadata_ApplyToClientOptions covers spec item 5: the client-side apply sets the identity only
// when it is unset, so a user-provided identity always wins.
func TestMetadata_ApplyToClientOptions(t *testing.T) {
	md := cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool", Revision: "rev-1"}

	t.Run("sets identity when unset", func(t *testing.T) {
		var o client.Options
		md.ApplyToClientOptions(&o)
		assert.Equal(t, "i-1@rev-1", o.Identity)
	})

	t.Run("preserves a user-provided identity", func(t *testing.T) {
		o := client.Options{Identity: "user-identity"}
		md.ApplyToClientOptions(&o)
		assert.Equal(t, "user-identity", o.Identity)
	})
}

// TestMetadata_ApplyToWorkerOptions covers spec item 5: the worker-side apply enables versioning,
// sets the deployment version, and pins the default versioning behavior. On incomplete metadata it
// returns an error and leaves the options unchanged.
func TestMetadata_ApplyToWorkerOptions(t *testing.T) {
	t.Run("enables pinned deployment versioning", func(t *testing.T) {
		md := cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool", Revision: "rev-1"}
		var o worker.Options
		require.NoError(t, md.ApplyToWorkerOptions(&o))

		assert.True(t, o.DeploymentOptions.UseVersioning)
		assert.Equal(t,
			worker.WorkerDeploymentVersion{DeploymentName: "my-pool", BuildID: "rev-1"},
			o.DeploymentOptions.Version)
		assert.Equal(t, workflow.VersioningBehaviorPinned, o.DeploymentOptions.DefaultVersioningBehavior)
	})

	t.Run("errors and leaves options unchanged when metadata incomplete", func(t *testing.T) {
		md := cloudrun.Metadata{InstanceID: "i-1", Name: "my-pool"} // revision empty
		var o worker.Options
		err := md.ApplyToWorkerOptions(&o)
		require.Error(t, err)
		assert.Equal(t, worker.DeploymentOptions{}, o.DeploymentOptions)
	})
}
