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
	"go.temporal.io/sdk/internal/common/cache"
)

// A shared cache workers can use to store workflow state
type workerCache struct {
	workflowCache cache.Cache
}

// Returns a cache with the provided size
func newWorkerCache(size int) workerCache {
	return workerCache{
		workflowCache: cache.New(size, &cache.Options{
			RemovedFunc: func(cachedEntity interface{}) {
				wec := cachedEntity.(*workflowExecutionContextImpl)
				wec.onEviction()
			},
		}),
	}
}

// TODO: Cache sharing -- should be maintained? Why are multiple workers in the same proc a thing?
//   generally update docstring

func (wc *workerCache) getWorkflowCache() cache.Cache {
	return wc.workflowCache
}

func (wc *workerCache) getWorkflowContext(runID string) *workflowExecutionContextImpl {
	o := wc.getWorkflowCache().Get(runID)
	if o == nil {
		return nil
	}
	wec := o.(*workflowExecutionContextImpl)
	return wec
}

func (wc *workerCache) putWorkflowContext(runID string, wec *workflowExecutionContextImpl) (*workflowExecutionContextImpl, error) {
	existing, err := wc.getWorkflowCache().PutIfNotExist(runID, wec)
	if err != nil {
		return nil, err
	}
	return existing.(*workflowExecutionContextImpl), nil
}

func (wc *workerCache) removeWorkflowContext(runID string) {
	wc.getWorkflowCache().Delete(runID)
}
