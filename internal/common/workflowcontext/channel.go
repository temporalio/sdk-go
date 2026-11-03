package workflowcontext

import "time"

// SendChannel is a write only view of the Channel
type SendChannel interface {
	// Name returns the name of the Channel.
	// If the Channel was retrieved from a GetSignalChannel call, Name returns the signal name.
	//
	// A Channel created without an explicit name will use a generated name by the SDK and
	// is not deterministic.
	Name() string

	// Send blocks until the data is sent.
	Send(ctx Context, v any)

	// SendAsync try to send without blocking. It returns true if the data was sent, otherwise it returns false.
	SendAsync(v any) (ok bool)

	// Close close the Channel, and prohibit subsequent sends.
	Close()
}

// ReceiveChannel is a read only view of the Channel
type ReceiveChannel interface {
	// Name returns the name of the Channel.
	// If the Channel was retrieved from a GetSignalChannel call, Name returns the signal name.
	//
	// A Channel created without an explicit name will use a generated name by the SDK and
	// is not deterministic.
	Name() string

	// Receive blocks until it receives a value, and then assigns the received value to the provided pointer.
	// Returns false when Channel is closed.
	// Parameter valuePtr is a pointer to the expected data structure to be received. For example:
	//  var v string
	//  c.Receive(ctx, &v)
	//
	// Note, values should not be reused for extraction here because merging on
	// top of existing values may result in unexpected behavior similar to
	// json.Unmarshal.
	Receive(ctx Context, valuePtr any) (more bool)

	// ReceiveWithTimeout blocks up to timeout until it receives a value, and then assigns the received value to the
	// provided pointer.
	// Returns more value of false when Channel is closed.
	// Returns ok value of false when no value was found in the channel for the duration of timeout or
	// the ctx was canceled.
	// The valuePtr is not modified if ok is false.
	// Parameter valuePtr is a pointer to the expected data structure to be received. For example:
	//  var v string
	//  c.ReceiveWithTimeout(ctx, time.Minute, &v)
	//
	// Note, values should not be reused for extraction here because merging on
	// top of existing values may result in unexpected behavior similar to
	// json.Unmarshal.
	ReceiveWithTimeout(ctx Context, timeout time.Duration, valuePtr any) (ok, more bool)

	// ReceiveAsync tries to receive from a Channel without blocking. If there is data available, it
	// assigns the data to valuePtr and returns true. Otherwise, it returns false immediately.
	//
	// Note, values should not be reused for extraction here because merging on
	// top of existing values may result in unexpected behavior similar to
	// json.Unmarshal.
	ReceiveAsync(valuePtr any) (ok bool)

	// ReceiveAsyncWithMoreFlag is the same as ReceiveAsync but with an extra return value more that indicates
	// whether the channel contains more data. more is false when the channel is closed.
	//
	// Note, values should not be reused for extraction here because merging on top of existing values may result in
	// unexpected behavior similar to json.Unmarshal.
	ReceiveAsyncWithMoreFlag(valuePtr any) (ok bool, more bool)

	// Len returns the number of buffered messages plus the number of blocked Send calls.
	Len() int
}

// Channel must be used by workflow code instead of native go channels.
// Use workflow.NewChannel(ctx) method to create Channel instance.
type Channel interface {
	SendChannel
	ReceiveChannel
}
