package internal

import (
	"context"

	"go.temporal.io/sdk/log"
)

// NexusOperationContext is an internal only struct that holds fields used by the temporalnexus functions.
type NexusOperationContext struct {
	Client    Client
	TaskQueue string
	Log       log.Logger
}

// nexusOperationContextKey is a key for associating a [NexusOperationContext] with a [context.Context].
var nexusOperationContextKey = struct{}{}

// NexusOperationContextFromGoContext gets the [NexusOperationContext] associated with the given [context.Context].
func NexusOperationContextFromGoContext(ctx context.Context) (nctx *NexusOperationContext, ok bool) {
	nctx, ok = ctx.Value(nexusOperationContextKey).(*NexusOperationContext)
	return
}
