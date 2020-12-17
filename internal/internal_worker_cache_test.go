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

package internal

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type (
	WorkerCacheSuite struct {
		suite.Suite
	}
)

func TestWorkerCacheTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerCacheSuite))
}

func (s *WorkerCacheSuite) TestCreateAndFree() {
	cache := getWorkflowCache()
	s.NotNil(cache)
	s.NotNil(workflowCache)
	s.Equal(workerRefcount.Load(), int32(1))
	cache2 := getWorkflowCache()
	s.NotNil(cache2)
	s.NotNil(workflowCache)
	s.Equal(workerRefcount.Load(), int32(2))
	cache.Close()
	s.Equal(workerRefcount.Load(), int32(1))
	s.NotNil(workflowCache)
	cache2.Close()
	s.Equal(workerRefcount.Load(), int32(0))
	s.Nil(workflowCache)
}
