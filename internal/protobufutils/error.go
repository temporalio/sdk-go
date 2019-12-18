package protobufutils

import (
	"fmt"

	"github.com/gogo/protobuf/proto"
	"go.uber.org/yarpc/encoding/protobuf"
	"go.uber.org/yarpc/yarpcerrors"
	"google.golang.org/grpc/codes"
)

func NewError(code codes.Code) error {
	return NewErrorWithMessage(code, "")
}

func NewErrorWithMessage(code codes.Code, message string) error {
	return protobuf.NewError(toYARPCCode(code), message)
}

func NewErrorWithFailure(code codes.Code, message string, failure proto.Message) error {
	return protobuf.NewError(toYARPCCode(code), message, protobuf.WithErrorDetails(failure))
}

func IsOfCode(err error, codes ...codes.Code) bool {
	if err == nil {
		return false
	}

	errCode := yarpcerrors.FromError(err).Code()
	for _, code := range codes {
		if errCode == toYARPCCode(code) {
			return true
		}
	}

	return false
}

func GetFailure(err error) interface{} {
	details := protobuf.GetErrorDetails(err)
	if len(details) > 0 {
		return details[0]
	}

	return nil
}

func toYARPCCode(code codes.Code) yarpcerrors.Code {
	switch code {
	case codes.OK:
		return yarpcerrors.CodeOK
	case codes.Canceled:
		return yarpcerrors.CodeCancelled
	case codes.Unknown:
		return yarpcerrors.CodeUnknown
	case codes.InvalidArgument:
		return yarpcerrors.CodeInvalidArgument
	case codes.DeadlineExceeded:
		return yarpcerrors.CodeDeadlineExceeded
	case codes.NotFound:
		return yarpcerrors.CodeNotFound
	case codes.AlreadyExists:
		return yarpcerrors.CodeAlreadyExists
	case codes.PermissionDenied:
		return yarpcerrors.CodePermissionDenied
	case codes.ResourceExhausted:
		return yarpcerrors.CodeResourceExhausted
	case codes.FailedPrecondition:
		return yarpcerrors.CodeFailedPrecondition
	case codes.Aborted:
		return yarpcerrors.CodeAborted
	case codes.OutOfRange:
		return yarpcerrors.CodeOutOfRange
	case codes.Unimplemented:
		return yarpcerrors.CodeUnimplemented
	case codes.Internal:
		return yarpcerrors.CodeInternal
	case codes.Unavailable:
		return yarpcerrors.CodeUnavailable
	case codes.DataLoss:
		return yarpcerrors.CodeDataLoss
	case codes.Unauthenticated:
		return yarpcerrors.CodeUnauthenticated
	}

	panic(fmt.Sprintf("Unexpected error code: %v", code))
}
