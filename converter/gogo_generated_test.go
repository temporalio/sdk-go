// The MIT License
//
// Copyright (c) 2023 Temporal Technologies Inc.  All rights reserved.
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

// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: message.proto

package converter

import (
	fmt "fmt"
	proto "github.com/gogo/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

type Typegogo int32

const (
	Typegogo_TYPEGOGO_UNSPECIFIED Typegogo = 0
	Typegogo_TYPEGOGO_R           Typegogo = 1
	Typegogo_TYPEGOGO_S           Typegogo = 2
)

var Typegogo_name = map[int32]string{
	0: "TYPEGOGO_UNSPECIFIED",
	1: "TYPEGOGO_R",
	2: "TYPEGOGO_S",
}

var Typegogo_value = map[string]int32{
	"TYPEGOGO_UNSPECIFIED": 0,
	"TYPEGOGO_R":           1,
	"TYPEGOGO_S":           2,
}

func (x Typegogo) String() string {
	return proto.EnumName(Typegogo_name, int32(x))
}

func (Typegogo) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_33c57e4bae7b9afd, []int{0}
}

type Gogo struct {
	Name     string   `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Birthday int64    `protobuf:"varint,2,opt,name=birthday,proto3" json:"birthday,omitempty"`
	Phone    string   `protobuf:"bytes,3,opt,name=phone,proto3" json:"phone,omitempty"`
	Siblings int32    `protobuf:"varint,4,opt,name=siblings,proto3" json:"siblings,omitempty"`
	Spouse   bool     `protobuf:"varint,5,opt,name=spouse,proto3" json:"spouse,omitempty"`
	Money    float64  `protobuf:"fixed64,6,opt,name=money,proto3" json:"money,omitempty"`
	Type     Typegogo `protobuf:"varint,7,opt,name=type,proto3,enum=temporal.sdk.converter.Typegogo" json:"type,omitempty"`
	// Types that are valid to be assigned to Values:
	//
	//	*Gogo_ValueS
	//	*Gogo_ValueI
	//	*Gogo_ValueD
	Values               isGogo_Values `protobuf_oneof:"Values"`
	XXX_NoUnkeyedLiteral struct{}      `json:"-"`
	XXX_unrecognized     []byte        `json:"-"`
	XXX_sizecache        int32         `json:"-"`
}

func (m *Gogo) Reset()         { *m = Gogo{} }
func (m *Gogo) String() string { return proto.CompactTextString(m) }
func (*Gogo) ProtoMessage()    {}
func (*Gogo) Descriptor() ([]byte, []int) {
	return fileDescriptor_33c57e4bae7b9afd, []int{0}
}
func (m *Gogo) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_Gogo.Unmarshal(m, b)
}
func (m *Gogo) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_Gogo.Marshal(b, m, deterministic)
}
func (m *Gogo) XXX_Merge(src proto.Message) {
	xxx_messageInfo_Gogo.Merge(m, src)
}
func (m *Gogo) XXX_Size() int {
	return xxx_messageInfo_Gogo.Size(m)
}
func (m *Gogo) XXX_DiscardUnknown() {
	xxx_messageInfo_Gogo.DiscardUnknown(m)
}

var xxx_messageInfo_Gogo proto.InternalMessageInfo

type isGogo_Values interface {
	isGogo_Values()
}

type Gogo_ValueS struct {
	ValueS string `protobuf:"bytes,8,opt,name=value_s,json=valueS,proto3,oneof" json:"value_s,omitempty"`
}
type Gogo_ValueI struct {
	ValueI int32 `protobuf:"varint,9,opt,name=value_i,json=valueI,proto3,oneof" json:"value_i,omitempty"`
}
type Gogo_ValueD struct {
	ValueD float64 `protobuf:"fixed64,10,opt,name=value_d,json=valueD,proto3,oneof" json:"value_d,omitempty"`
}

func (*Gogo_ValueS) isGogo_Values() {}
func (*Gogo_ValueI) isGogo_Values() {}
func (*Gogo_ValueD) isGogo_Values() {}

func (m *Gogo) GetValues() isGogo_Values {
	if m != nil {
		return m.Values
	}
	return nil
}

func (m *Gogo) GetName() string {
	if m != nil {
		return m.Name
	}
	return ""
}

func (m *Gogo) GetBirthday() int64 {
	if m != nil {
		return m.Birthday
	}
	return 0
}

func (m *Gogo) GetPhone() string {
	if m != nil {
		return m.Phone
	}
	return ""
}

func (m *Gogo) GetSiblings() int32 {
	if m != nil {
		return m.Siblings
	}
	return 0
}

func (m *Gogo) GetSpouse() bool {
	if m != nil {
		return m.Spouse
	}
	return false
}

func (m *Gogo) GetMoney() float64 {
	if m != nil {
		return m.Money
	}
	return 0
}

func (m *Gogo) GetType() Typegogo {
	if m != nil {
		return m.Type
	}
	return Typegogo_TYPEGOGO_UNSPECIFIED
}

func (m *Gogo) GetValueS() string {
	if x, ok := m.GetValues().(*Gogo_ValueS); ok {
		return x.ValueS
	}
	return ""
}

func (m *Gogo) GetValueI() int32 {
	if x, ok := m.GetValues().(*Gogo_ValueI); ok {
		return x.ValueI
	}
	return 0
}

func (m *Gogo) GetValueD() float64 {
	if x, ok := m.GetValues().(*Gogo_ValueD); ok {
		return x.ValueD
	}
	return 0
}

// XXX_OneofWrappers is for the internal use of the proto package.
func (*Gogo) XXX_OneofWrappers() []interface{} {
	return []interface{}{
		(*Gogo_ValueS)(nil),
		(*Gogo_ValueI)(nil),
		(*Gogo_ValueD)(nil),
	}
}

func init() {
	proto.RegisterEnum("temporal.sdk.converter.Typegogo", Typegogo_name, Typegogo_value)
	proto.RegisterType((*Gogo)(nil), "temporal.sdk.converter.Gogo")
}

func init() { proto.RegisterFile("message.proto", fileDescriptor_33c57e4bae7b9afd) }

var fileDescriptor_33c57e4bae7b9afd = []byte{
	// 314 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x6c, 0x91, 0xc1, 0x4e, 0xc2, 0x40,
	0x10, 0x86, 0x59, 0x28, 0xa5, 0x4c, 0x22, 0x21, 0x1b, 0x42, 0x56, 0x4f, 0x1b, 0x2f, 0x56, 0x0f,
	0x25, 0x51, 0x6f, 0xde, 0x10, 0x44, 0x2e, 0x42, 0x16, 0x34, 0xd1, 0x0b, 0x29, 0x76, 0x52, 0x1a,
	0x68, 0xa7, 0xe9, 0x16, 0x92, 0x3e, 0x91, 0xaf, 0x69, 0x5a, 0x74, 0x89, 0x89, 0xb7, 0xff, 0xeb,
	0xd7, 0xd9, 0xfd, 0x27, 0x0b, 0x67, 0x31, 0x6a, 0xed, 0x87, 0xe8, 0xa5, 0x19, 0xe5, 0xc4, 0xfb,
	0x39, 0xc6, 0x29, 0x65, 0xfe, 0xce, 0xd3, 0xc1, 0xd6, 0xfb, 0xa4, 0xe4, 0x80, 0x59, 0x8e, 0xd9,
	0xe5, 0x57, 0x1d, 0xac, 0x09, 0x85, 0xc4, 0x39, 0x58, 0x89, 0x1f, 0xa3, 0x60, 0x92, 0xb9, 0x6d,
	0x55, 0x65, 0x7e, 0x01, 0xce, 0x3a, 0xca, 0xf2, 0x4d, 0xe0, 0x17, 0xa2, 0x2e, 0x99, 0xdb, 0x50,
	0x86, 0x79, 0x0f, 0x9a, 0xe9, 0x86, 0x12, 0x14, 0x8d, 0x6a, 0xe0, 0x08, 0xe5, 0x84, 0x8e, 0xd6,
	0xbb, 0x28, 0x09, 0xb5, 0xb0, 0x24, 0x73, 0x9b, 0xca, 0x30, 0xef, 0x83, 0xad, 0x53, 0xda, 0x6b,
	0x14, 0x4d, 0xc9, 0x5c, 0x47, 0xfd, 0x50, 0x79, 0x52, 0x4c, 0x09, 0x16, 0xc2, 0x96, 0xcc, 0x65,
	0xea, 0x08, 0xfc, 0x1e, 0xac, 0xbc, 0x48, 0x51, 0xb4, 0x24, 0x73, 0x3b, 0xb7, 0xd2, 0xfb, 0xbf,
	0xbf, 0xb7, 0x2c, 0x52, 0x0c, 0x29, 0x24, 0x55, 0xfd, 0xcd, 0xcf, 0xa1, 0x75, 0xf0, 0x77, 0x7b,
	0x5c, 0x69, 0xe1, 0x94, 0xbd, 0x9e, 0x6b, 0xca, 0xae, 0x3e, 0x2c, 0x4e, 0x2a, 0x12, 0xed, 0xb2,
	0x99, 0x51, 0xd3, 0x93, 0x0a, 0x04, 0x94, 0x1d, 0x8c, 0x1a, 0x0d, 0x1d, 0xb0, 0xdf, 0xca, 0xa4,
	0x6f, 0x46, 0xe0, 0xfc, 0x5e, 0xc6, 0x05, 0xf4, 0x96, 0xef, 0xf3, 0xf1, 0x64, 0x36, 0x99, 0xad,
	0x5e, 0x5f, 0x16, 0xf3, 0xf1, 0xe3, 0xf4, 0x69, 0x3a, 0x1e, 0x75, 0x6b, 0xbc, 0x03, 0x60, 0x8c,
	0xea, 0xb2, 0x3f, 0xbc, 0xe8, 0xd6, 0x87, 0xd7, 0x1f, 0x57, 0x21, 0x9d, 0x96, 0x89, 0x68, 0xa0,
	0x83, 0xed, 0xc0, 0xec, 0x33, 0x78, 0x30, 0x71, 0x6d, 0x57, 0x2f, 0x77, 0xf7, 0x1d, 0x00, 0x00,
	0xff, 0xff, 0x9c, 0x9c, 0x15, 0x25, 0xca, 0x01, 0x00, 0x00,
}
