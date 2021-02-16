package internal

import "context"

// AuthorizationTokenProvider is a function that can be provided to ConnectionOptions, returning a token that will
// be injected into the authorization metadata
type AuthorizationTokenProvider func(ctx context.Context) (string, error)
