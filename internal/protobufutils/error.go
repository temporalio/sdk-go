// Copyright (c) 2019 Temporal Technologies, Inc.
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

package protobufutils

import (
	"github.com/gogo/protobuf/proto"
	"github.com/gogo/status"
	"google.golang.org/grpc/codes"
)

// NewError creates new error for protobuf.
func NewError(code codes.Code) error {
	return NewErrorWithMessage(code, "")
}

// NewErrorWithMessage creates new error for protobuf with message.
func NewErrorWithMessage(code codes.Code, message string) error {
	return status.New(code, message).Err()
}

// NewErrorWithFailure creates new error for protobuf with message and failure details.
func NewErrorWithFailure(code codes.Code, message string, failure proto.Message) error {
	st, _ := status.New(code, message).WithDetails(failure)
	return st.Err()
}

// GetCode returns code for protobuf error.
func GetCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	return status.Convert(err).Code()
}

// GetMessage returns message for protobuf error.
func GetMessage(err error) string {
	if err == nil {
		return ""
	}

	return status.Convert(err).Message()
}

// GetFailure returns failure details for protobuf error.
func GetFailure(err error) interface{} {
	details := status.Convert(err).Details()
	if len(details) > 0 {
		return details[0]
	}

	return nil
}
