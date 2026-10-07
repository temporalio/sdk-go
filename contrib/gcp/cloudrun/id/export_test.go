package id

import "net/http"

// NewTestPlugin builds a [CloudRunIDPlugin] with the seams the tests need: a pre-fetched metadata
// value (skips the network fetch), or an overridden metadata server URL / HTTP client. It lives in a
// _test.go file, so it is compiled only into tests and is not part of the public API.
func NewTestPlugin(metadata *Metadata, metadataURL string, httpClient *http.Client) *CloudRunIDPlugin {
	return &CloudRunIDPlugin{
		metadata:    metadata,
		metadataURL: metadataURL,
		httpClient:  httpClient,
	}
}
