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

package log

import (
	"fmt"
)

type MockLogger struct {
	lines         []string
	globalKeyvals string
}

func NewMockLogger() *MockLogger {
	return &MockLogger{}
}

func (l *MockLogger) println(level, msg string, keyvals []interface{}) {
	// To avoid extra space when globalKeyvals is not specified.
	if l.globalKeyvals == "" {
		l.lines = append(l.lines, fmt.Sprintln(append([]interface{}{level, msg}, keyvals...)...))
	} else {
		l.lines = append(l.lines, fmt.Sprintln(append([]interface{}{level, msg, l.globalKeyvals}, keyvals...)...))
	}
}

func (l *MockLogger) Debug(msg string, keyvals ...interface{}) {
	l.println("DEBUG", msg, keyvals)
}

func (l *MockLogger) Info(msg string, keyvals ...interface{}) {
	l.println("INFO", msg, keyvals)
}

func (l *MockLogger) Warn(msg string, keyvals ...interface{}) {
	l.println("WARN", msg, keyvals)
}

func (l *MockLogger) Error(msg string, keyvals ...interface{}) {
	l.println("ERROR", msg, keyvals)
}

func (l *MockLogger) With(keyvals ...interface{}) Logger {
	logger := &MockLogger{}

	if l.globalKeyvals == "" {
		logger.globalKeyvals = fmt.Sprint(keyvals...)
	} else {
		logger.globalKeyvals = fmt.Sprint(l.globalKeyvals, fmt.Sprint(keyvals...))
	}

	return logger
}

func (l *MockLogger) Lines() []string {
	return l.lines
}
