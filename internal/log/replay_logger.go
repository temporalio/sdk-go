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

type (
	// ReplayLogger is a wrapper around Logger that will be aware of replay
	ReplayLogger struct {
		logger                Logger
		isReplay              *bool // pointer to bool that indicate if it is in replay mode
		enableLoggingInReplay *bool // pointer to bool that indicate if logging is enabled in replay mode
	}
)

func NewReplayLogger(logger Logger, isReplay *bool, enableLoggingInReplay *bool) Logger {
	return &ReplayLogger{
		logger:                logger,
		isReplay:              isReplay,
		enableLoggingInReplay: enableLoggingInReplay,
	}
}

func (l *ReplayLogger) check() bool {
	return !*l.isReplay || *l.enableLoggingInReplay
}

func (l *ReplayLogger) Debug(msg string, keyvals ...interface{}) {
	if l.check() {
		l.logger.Debug(msg, keyvals...)
	}
}

func (l *ReplayLogger) Info(msg string, keyvals ...interface{}) {
	if l.check() {
		l.logger.Info(msg, keyvals...)
	}
}

func (l *ReplayLogger) Warn(msg string, keyvals ...interface{}) {
	if l.check() {
		l.logger.Warn(msg, keyvals...)
	}
}

func (l *ReplayLogger) Error(msg string, keyvals ...interface{}) {
	if l.check() {
		l.logger.Error(msg, keyvals...)
	}
}

func (l *ReplayLogger) With(keyvals ...interface{}) Logger {
	return NewReplayLogger(l.logger.With(keyvals), l.isReplay, l.enableLoggingInReplay)
}
