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

import (
	"log"
	mathrand "math/rand"
	"net/http"
	"time"
)

func CallsTime() { // want CallsTime:"calls non-deterministic function time.Now"
	time.Now()
}

func CallsTimeTransitively() { // want CallsTimeTransitively:"calls non-deterministic function a.CallsTime"
	CallsTime()
}

func CallsOtherTimeCall() { // want CallsOtherTimeCall:"calls non-deterministic function time.Until"
	// Marked non-deterministic because it calls time.Now internally
	time.Until(time.Time{})
}

func Recursion() {
	Recursion()
}

func RecursionWithTimeCall() { // want RecursionWithTimeCall:"calls non-deterministic function a.CallsTimeTransitively"
	Recursion()
	CallsTimeTransitively()
}

func MultipleCalls() { // want MultipleCalls:"calls non-deterministic function time.Now, calls non-deterministic function a.CallsTime"
	time.Now()
	CallsTime()
}

func BadCall() { // want BadCall:"declared non-deterministic"
	Recursion()
}

func IgnoredCall() {
	time.Now()
}

func IgnoredCallTransitive() {
	IgnoredCall()
}

func CallsLog() { // want CallsLog:"calls non-deterministic function log.Println"
	log.Println()
}

func CallsMathRandom() { // want CallsMathRandom:"calls non-deterministic function math/rand.Int"
	mathrand.Int()
}

func CallsHTTP() { // want CallsHTTP:"calls non-deterministic function net/http.Get"
	http.Get("http://example.com")
}
