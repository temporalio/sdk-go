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
	"sync"

	"go.uber.org/atomic"

	"go.temporal.io/sdk/internal/common/cache"
)

// A shared cache workers can use to store workflow state. The cache is expected to be initialized with the first worker
// to be instantiated, and freed with the last worker to terminate. IE: All workers have a pointer to it.
type workerCache struct {
	workflowCache cache.Cache
}

var workflowCache *workerCache
var stickyCacheSize = defaultStickyCacheSize
var stickyCacheLock sync.Mutex

// Holds the number of outstanding references to the cache so we can zero it out if all workers are dead
var workerRefcount atomic.Int32

// SetStickyWorkflowCacheSize sets the cache size for sticky workflow cache. Sticky workflow execution is the affinity
// between workflow tasks of a specific workflow execution to a specific worker. The affinity is set if sticky execution
// is enabled via Worker.Options (It is enabled by default unless disabled explicitly). The benefit of sticky execution
// is that workflow does not have to reconstruct the state by replaying from beginning of history events. But the cost
// is it consumes more memory as it rely on caching workflow execution's running state on the worker. The cache is shared
// between workers running within same process. This must be called before any worker is started. If not called, the
// default size of 10K (might change in future) will be used.
func SetStickyWorkflowCacheSize(cacheSize int) {
	stickyCacheLock.Lock()
	defer stickyCacheLock.Unlock()
	if workflowCache != nil {
		panic("cache already created, please set cache size before worker starts.")
	}
	stickyCacheSize = cacheSize
}

// Returns a pointer to the workerCache, and increases the outstanding refcount by one. Callers *must* call
// workerCache.Close when done.
func getWorkflowCache() *workerCache {
	rcount := workerRefcount.Load()
	if rcount == 0 {
		stickyCacheLock.Lock()
		defer stickyCacheLock.Unlock()
		workflowCache = &workerCache{workflowCache: cache.New(stickyCacheSize, &cache.Options{
			RemovedFunc: func(cachedEntity interface{}) {
				wc := cachedEntity.(*workflowExecutionContextImpl)
				wc.onEviction()
			},
		})}
	}
	workerRefcount.Add(1)
	return workflowCache
}

func (wc *workerCache) Close() {
	rcount := workerRefcount.Sub(1)
	if rcount == 0 {
		// Delete cache if no more outstanding references
		stickyCacheLock.Lock()
		defer stickyCacheLock.Unlock()
		workflowCache = nil
	}
}

func (wc *workerCache) getWorkflowContext(runID string) *workflowExecutionContextImpl {
	o := wc.workflowCache.Get(runID)
	if o == nil {
		return nil
	}
	wec := o.(*workflowExecutionContextImpl)
	return wec
}

func (wc *workerCache) putWorkflowContext(runID string, wec *workflowExecutionContextImpl) (*workflowExecutionContextImpl, error) {
	existing, err := wc.workflowCache.PutIfNotExist(runID, wec)
	if err != nil {
		return nil, err
	}
	return existing.(*workflowExecutionContextImpl), nil
}

func (wc *workerCache) removeWorkflowContext(runID string) {
	wc.workflowCache.Delete(runID)
}
