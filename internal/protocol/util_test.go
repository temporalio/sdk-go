package protocol_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	protocolpb "go.temporal.io/api/protocol/v1"
	updatepb "go.temporal.io/api/update/v1"
	"go.temporal.io/sdk/internal/common/serializer"
	"go.temporal.io/sdk/internal/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestNameFromMessage(t *testing.T) {
	msg := &protocolpb.Message{Body: &anypb.Any{}}
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
	require.Implements(t, new(serializer.Marshaler), msg)
	require.Panics(t, func() { protocol.MustMarshalAny(msg) })
}
