package cadence

import (
	"github.com/stretchr/testify/require"
	"github.com/uber/tchannel-go"
	"testing"
	"time"
)

func TestTChannelBuilderOptions(t *testing.T) {
	builder := tchannel.NewContextBuilder(defaultRpcTimeout)
	require.Equal(t, defaultRpcTimeout, builder.Timeout)

	opt1 := tchanTimeout(time.Minute)
	opt2 := tchanRetryOption(retryNeverOptions)

	opt1(builder)
	opt2(builder)

	require.Equal(t, time.Minute, builder.Timeout)
	require.Equal(t, retryNeverOptions, builder.RetryOptions)
}
