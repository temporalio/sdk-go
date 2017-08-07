// Copyright (c) 2017 Uber Technologies, Inc.
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

package common

import (
	"github.com/apache/thrift/lib/go/thrift"
)

// TSerialize is used to serialize thrift TStruct to []byte
func TSerialize(t thrift.TStruct) (b []byte, err error) {
	return thrift.NewTSerializer().Write(t)
}

// TListSerialize is used to serialize list of thrift TStruct to []byte
func TListSerialize(ts []thrift.TStruct) (b []byte, err error) {
	if ts == nil {
		return
	}

	t := thrift.NewTSerializer()
	t.Transport.Reset()

	// NOTE: we don't write any markers as thrift by design being a streaming protocol doesn't
	// recommend writing length.

	for _, v := range ts {
		if e := v.Write(t.Protocol); e != nil {
			err = thrift.PrependError("error writing TStruct: ", e)
			return
		}
	}

	if err = t.Protocol.Flush(); err != nil {
		return
	}

	if err = t.Transport.Flush(); err != nil {
		return
	}

	b = t.Transport.Bytes()
	return
}

// TDeserialize is used to deserialize []byte to thrift TStruct
func TDeserialize(t thrift.TStruct, b []byte) (err error) {
	return thrift.NewTDeserializer().Read(t, b)
}

// TListDeserialize is used to deserialize []byte to list of thrift TStruct
func TListDeserialize(ts []thrift.TStruct, b []byte) (err error) {
	t := thrift.NewTDeserializer()
	err = nil
	if _, err = t.Transport.Write(b); err != nil {
		return
	}

	for i := 0; i < len(ts); i++ {
		if e := ts[i].Read(t.Protocol); e != nil {
			err = thrift.PrependError("error reading TStruct: ", e)
			return
		}
	}

	return
}
