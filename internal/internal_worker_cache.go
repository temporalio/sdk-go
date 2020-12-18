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
	"runtime"
	"sync"

	"go.temporal.io/sdk/internal/common/cache"
)

// Each worker can use an instance of workerCache to hold cached data. The contents of this struct should always
// be pointers for any data shared with other workers, and owned values for any instance-specific caches.
type workerCache struct {
	workflowCache *cache.Cache
}

// A shared cache workers can use to store workflow state. The cache is expected to be initialized with the first worker
// to be instantiated, and freed with the last worker to terminate. IE: All workers have a pointer to it.
//
// Don't touch this except via methods on workerCache.
var workflowCache cache.Cache
var stickyCacheSize = defaultStickyCacheSize
var stickyCacheLock sync.Mutex

// Holds the number of outstanding workerCache instances so we can zero out shared caches if all workers are dead. Lock
// should be held while manipulating.
var workerRefcount int

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

// Creates a new workerCache, and increases workerRefcount by one. Instances of workerCache decrement the refcounter as
// a hook to runtime.SetFinalizer (ie: When they are freed by the GC). When there are no reachable instances of
// workerCache, shared caches will be cleared
func newWorkerCache() workerCache {
	stickyCacheLock.Lock()
	defer stickyCacheLock.Unlock()
	if workerRefcount == 0 {
		newcache := cache.New(stickyCacheSize, &cache.Options{
			RemovedFunc: func(cachedEntity interface{}) {
				wc := cachedEntity.(*workflowExecutionContextImpl)
				wc.onEviction()
			},
		})
		workflowCache = newcache
	}
	workerRefcount++
	newWorkerCache := workerCache{
		&workflowCache,
	}
	runtime.SetFinalizer(&newWorkerCache, func(wc *workerCache) {
		wc.close()
	})
	return newWorkerCache
}

func (wc workerCache) getWorkflowCache() cache.Cache {
	return *wc.workflowCache
}

func (wc workerCache) close() {
	stickyCacheLock.Lock()
	defer stickyCacheLock.Unlock()
	if workerRefcount == 0 {
		// Delete cache if no more outstanding references
		workflowCache = nil
	}
}

func (wc workerCache) getWorkflowContext(runID string) *workflowExecutionContextImpl {
	o := (*wc.workflowCache).Get(runID)
	if o == nil {
		return nil
	}
	wec := o.(*workflowExecutionContextImpl)
	return wec
}

func (wc workerCache) putWorkflowContext(runID string, wec *workflowExecutionContextImpl) (*workflowExecutionContextImpl, error) {
	existing, err := (*wc.workflowCache).PutIfNotExist(runID, wec)
	if err != nil {
		return nil, err
	}
	return existing.(*workflowExecutionContextImpl), nil
}

func (wc workerCache) removeWorkflowContext(runID string) {
	(*wc.workflowCache).Delete(runID)
}
