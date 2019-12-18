package protobufutils

import (
	"github.com/gogo/protobuf/proto"
	"go.uber.org/yarpc/encoding/protobuf"
	"go.uber.org/yarpc/yarpcerrors"
	"google.golang.org/grpc/codes"
)

func NewError(code codes.Code) error {
	return NewErrorWithMessage(code, "")
}

func NewErrorWithMessage(code codes.Code, message string) error {
	return protobuf.NewError(yarpcerrors.Code(code), message)
}

func NewErrorWithFailure(code codes.Code, message string, failure proto.Message) error {
	return protobuf.NewError(yarpcerrors.Code(code), message, protobuf.WithErrorDetails(failure))
}

func GetCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	return codes.Code(yarpcerrors.FromError(err).Code())
}

func GetMessage(err error) string {
	if err == nil {
		return ""
	}

	return yarpcerrors.FromError(err).Message()
}

func GetFailure(err error) interface{} {
	details := protobuf.GetErrorDetails(err)
	if len(details) > 0 {
		return details[0]
	}

	return nil
}
