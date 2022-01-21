// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
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

package a

import "time"

type SomeStruct struct{}

func (SomeStruct) NonPtrReceiver() { // want NonPtrReceiver:"calls non-deterministic function time\\.Now"
	time.Now()
}

func (*SomeStruct) PtrReceiver() { // want PtrReceiver:"calls non-deterministic function time\\.Now"
	time.Now()
}

type SomeInterface interface {
	BadCall() // want BadCall:"declared non-deterministic"
}

func CallsNonPtrReceiver() { // want CallsNonPtrReceiver:"calls non-deterministic function \\(a\\.SomeStruct\\).NonPtrReceiver"
	SomeStruct{}.NonPtrReceiver()
}

func CallsPtrReceiver() { // want CallsPtrReceiver:"calls non-deterministic function \\(\\*a\\.SomeStruct\\)\\.PtrReceiver"
	var s SomeStruct
	s.PtrReceiver()
}

func CallsInterface() { // want CallsInterface:"calls non-deterministic function \\(a\\.SomeInterface\\)\\.BadCall"
	var iface SomeInterface
	iface.BadCall()
}
