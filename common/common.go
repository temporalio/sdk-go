package common

import (
	"time"

	"github.com/uber/tchannel-go"

	"golang.org/x/net/context"
)

// RetryNeverOptions - Never retry the connection
var RetryNeverOptions = &tchannel.RetryOptions{
	RetryOn: tchannel.RetryNever,
}

// RetryDefaultOptions - retry with default options.
var RetryDefaultOptions = &tchannel.RetryOptions{
	RetryOn: tchannel.RetryDefault,
}

// NewTChannelContext - Get a tchannel context
func NewTChannelContext(timeout time.Duration, retryOptions *tchannel.RetryOptions) (tchannel.ContextWithHeaders, context.CancelFunc) {
	return tchannel.NewContextBuilder(timeout).
		SetRetryOptions(retryOptions).
		Build()
}
