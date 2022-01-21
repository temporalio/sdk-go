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

func StartsGoroutine() { // want StartsGoroutine:"starts goroutine"
	go func() {}()
}

func StartsGoroutineTransitively() { // want StartsGoroutineTransitively:"calls non-deterministic function a.StartsGoroutine"
	StartsGoroutine()
}

func ChannelSend() { // want ChannelSend:"sends to channel"
	ch := make(chan string, 1)
	ch <- "foo"
}

func ChannelSendInSelect() { // want ChannelSendInSelect:"sends to channel"
	ch := make(chan string, 1)
	select {
	case ch <- "foo":
	default:
	}
}

func ChannelRecv() { // want ChannelRecv:"receives from channel"
	ch := make(chan string, 1)
	<-ch
}

func ChannelRecvAssign() { // want ChannelRecvAssign:"receives from channel"
	ch := make(chan string, 1)
	_ = <-ch
}

func ChannelRecvInSelect() { // want ChannelRecvInSelect:"receives from channel"
	ch := make(chan string, 1)
	select {
	case <-ch:
	default:
	}
}

func ChannelRange() { // want ChannelRange:"iterates over channel"
	ch := make(chan string, 1)
	for range ch {
	}
}

type MyChan chan string

func ChannelRangeWrappedType() { // want ChannelRangeWrappedType:"iterates over channel"
	ch := make(MyChan, 1)
	for range ch {
	}
}
