package internal

type (
	// Options for when payload sizes exceed limits.
	//
	// Exposed as: [go.temporal.io/sdk/client.PayloadLimitOptions]
	PayloadLimitOptions struct {
		// The limit (in bytes) at which a payload size warning is logged.
		PayloadSizeWarning int
	}
)
