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

package a //want package:"\\d+ non-deterministic vars/funcs"

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"
)

var BadVar time.Time // want BadVar:"declared non-deterministic"

func AccessesStdout() { // want AccessesStdout:"accesses non-deterministic var os.Stdout"
	os.Stdout.Write([]byte("Hello"))
}

func AccessesStdoutTransitively() { // want AccessesStdoutTransitively:"calls non-deterministic function a.AccessesStdout"
	AccessesStdout()
}

func CallsOtherStdoutCall() { // want CallsOtherStdoutCall:"calls non-deterministic function fmt.Println"
	fmt.Println()
}

func AccessesBadVar() { // want AccessesBadVar:"accesses non-deterministic var a.BadVar"
	BadVar.Day()
}

func AccessesIgnoredStderr() {
	os.Stderr.Write([]byte("Hello"))
}

func AccessesCryptoRandom() { // want AccessesCryptoRandom:"accesses non-deterministic var crypto/rand.Reader"
	rand.Reader.Read(nil)
}

func AccessesCryptoRandomTransitively() { // want AccessesCryptoRandomTransitively:"calls non-deterministic function crypto/rand.Read"
	rand.Read(nil)
}
