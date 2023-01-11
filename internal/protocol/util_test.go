// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package protocol_test

import (
	"errors"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/require"
	protocolpb "go.temporal.io/api/protocol/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/sdk/internal/protocol"
)

func TestNameFromMessage(t *testing.T) {
	msg := &protocolpb.Message{Body: &types.Any{}}
	_, err := protocol.NameFromMessage(msg)
	require.Error(t, err)

	msg.Body = protocol.MustMarshalAny(&updatepb.Request{})
	name, err := protocol.NameFromMessage(msg)
	require.NoError(t, err)
	require.Equal(t, "temporal.api.update.v1", name)
}

type IllTemperedProtoMessage struct{ proto.Message }

func (i *IllTemperedProtoMessage) Marshal() ([]byte, error) {
	return nil, errors.New("rekt")
}

func TestMustMarshalAny(t *testing.T) {
	msg := &IllTemperedProtoMessage{}
	require.Implements(t, new(proto.Marshaler), msg)
	require.Panics(t, func() { protocol.MustMarshalAny(msg) })
}
