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

import "fmt"

func IgnoreSameLine() { // want IgnoreSameLine:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	fmt.Print("Do not ignore this")
	fmt.Printf("Ignore this") //workflowcheck:ignore
	fmt.Println("Do not ignore this")
}

func IgnoreAboveLine() { // want IgnoreAboveLine:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	fmt.Print("Do not ignore this")
	//workflowcheck:ignore
	fmt.Printf("Ignore this")
	fmt.Println("Do not ignore this")
}

//workflowcheck:ignore
func IgnoreEntireFunction() {
	fmt.Print("Do not ignore this")
	fmt.Printf("Ignore this")
	fmt.Println("Do not ignore this")
}

func IgnoreFunctionTransitively() {
	IgnoreEntireFunction()
}

func IgnoreBlock() { // want IgnoreBlock:"calls non-deterministic function fmt.Print, calls non-deterministic function fmt.Println"
	for i := 0; i < 1; i++ {
		fmt.Print("Do not ignore this")
	}
	//workflowcheck:ignore
	for i := 0; i < 1; i++ {
		fmt.Printf("Ignore this")
	}
	for i := 0; i < 1; i++ {
		fmt.Println("Do not ignore this")
	}
}
