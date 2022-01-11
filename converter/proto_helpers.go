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

package converter

import (
	"fmt"

	gogoproto "github.com/gogo/protobuf/proto"
	"go.temporal.io/api/common/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func GetProtoMessageName(protoMessage *protoreflect.ProtoMessage) string {
	return string((*protoMessage).ProtoReflect().Descriptor().FullName())
}

func checkMessageType(payload *common.Payload, valueType string) {
	messageTypeBytes, ok := payload.Metadata[MetadataMessageType]
	messageType := string(messageTypeBytes)
	if (ok && messageType != valueType) {
		fmt.Println(`WARN Encoded protobuf message type "` + messageType	+ `" does not match value type "` + valueType + `"`);
	}
}

func CheckProtoMessageType(payload *common.Payload, protoMessage *protoreflect.ProtoMessage) {
	checkMessageType(payload, GetProtoMessageName(protoMessage))
}

func CheckGogoprotoMessageType(payload *common.Payload, protoMessage *gogoproto.Message) {
	checkMessageType(payload, gogoproto.MessageName(*protoMessage))
}