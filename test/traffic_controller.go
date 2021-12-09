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

package test

import (
	"context"
	"strings"
	"sync"

	ilog "go.temporal.io/sdk/internal/log"
	"go.temporal.io/sdk/log"
)

const FailAllAttempts = -1

type SimpleTrafficController struct {
	totalCalls   map[string]int
	allowedCalls map[string]int
	failAttempts map[string]map[int]error // maps operation to individual attempts and corresponding errors
	logger       log.Logger
	lock         sync.RWMutex
}

func NewSimpleTrafficController() *SimpleTrafficController {
	return &SimpleTrafficController{
		totalCalls:   make(map[string]int),
		allowedCalls: make(map[string]int),
		failAttempts: make(map[string]map[int]error),
		lock:         sync.RWMutex{},
		logger:       ilog.NewDefaultLogger(),
	}
}

func (tc *SimpleTrafficController) CheckCallAllowed(_ context.Context, method string, _, _ interface{}) error {
	// Name of the API being called
	operation := method[strings.LastIndex(method, "/")+1:]
	tc.lock.Lock()
	defer tc.lock.Unlock()
	var err error
	attempt := tc.totalCalls[operation]
	if _, ok := tc.failAttempts[operation]; ok && (tc.failAttempts[operation][attempt] != nil || tc.failAttempts[operation][FailAllAttempts] != nil) {
		err = tc.failAttempts[operation][attempt]
		if err == nil {
			err = tc.failAttempts[operation][FailAllAttempts]
		}
		tc.logger.Debug("Failing API call for", "operation:", operation, "call:", attempt)
	}
	tc.totalCalls[operation]++
	if err == nil {
		tc.allowedCalls[operation]++
	}
	return err
}

func (tc *SimpleTrafficController) AddError(operation string, err error, failAttempts ...int) {
	tc.lock.Lock()
	defer tc.lock.Unlock()
	if _, ok := tc.failAttempts[operation]; !ok {
		tc.failAttempts[operation] = make(map[int]error)
		for _, attempt := range failAttempts {
			tc.failAttempts[operation][attempt] = err
		}
	}
}
