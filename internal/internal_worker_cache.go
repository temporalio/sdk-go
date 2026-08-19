package internal

import (
	"runtime"
	"sync"

	"go.temporal.io/sdk/internal/common/cache"
)

// A WorkerCache instance is held by each worker to hold cached data. The contents of this struct should always be
// pointers for any data shared with other workers, and owned values for any instance-specific caches.
type WorkerCache struct {
	workflowCache        cache.Cache
	maxWorkflowCacheSize int
}

// workerCacheLease owns one reference to a shared cache generation. Workflow
// cache entries must not retain this lease, or they can prevent final release.
type workerCacheLease struct {
	sharedCache *sharedWorkerCache
	lock        *sync.Mutex
	releaseOnce sync.Once
}

// A container for data workers in this process may want to share with eachother
type sharedWorkerCache struct {
	// Count of live workers
	workerRefcount int

	// A cache workers can use to store workflow state.
	workflowCache cache.Cache
	// Max size for the cache
	maxWorkflowCacheSize int
}

// The current cache generation shared by live workers. When its owner count
// reaches zero, it is detached before being cleared. Do not manipulate without
// holding sharedWorkerCacheLock.
var sharedWorkerCachePtr = &sharedWorkerCache{}
var sharedWorkerCacheLock sync.Mutex

// Must be set before spawning any workers
var desiredWorkflowCacheSize = defaultStickyCacheSize

// SetStickyWorkflowCacheSize sets the cache size for sticky workflow cache. Sticky workflow execution is the affinity
// between workflow tasks of a specific workflow execution to a specific worker. The benefit of sticky execution is that
// the workflow does not have to reconstruct state by replaying history from the beginning. The cache is shared between
// workers running within same process. This must be called before any worker is started. If not called, the default
// size of 10K (which may change) will be used.
func SetStickyWorkflowCacheSize(cacheSize int) {
	sharedWorkerCacheLock.Lock()
	defer sharedWorkerCacheLock.Unlock()
	desiredWorkflowCacheSize = cacheSize
}

// PurgeStickyWorkflowCache resets the sticky workflow cache. This must be called only when all workers are stopped.
func PurgeStickyWorkflowCache() {
	sharedWorkerCacheLock.Lock()
	defer sharedWorkerCacheLock.Unlock()

	if sharedWorkerCachePtr.workflowCache != nil {
		sharedWorkerCachePtr.workflowCache.Clear()
	}
}

// NewWorkerCache creates a cache handle and a lease for its shared generation.
// The owner must release the lease when its lifecycle ends. A finalizer is kept
// only as a fallback for owners that are abandoned without deterministic cleanup.
func NewWorkerCache() (*WorkerCache, *workerCacheLease) {
	sharedWorkerCacheLock.Lock()
	desiredWorkflowCacheSize := desiredWorkflowCacheSize
	sharedWorkerCacheLock.Unlock()

	return newWorkerCache(sharedWorkerCachePtr, &sharedWorkerCacheLock, desiredWorkflowCacheSize)
}

// This private version allows us to test functionality without affecting the global shared cache.
func newWorkerCache(storeIn *sharedWorkerCache, lock *sync.Mutex, cacheSize int) (*WorkerCache, *workerCacheLease) {
	lock.Lock()
	defer lock.Unlock()

	if storeIn == nil {
		panic("Provided sharedWorkerCache pointer must not be nil")
	}

	if storeIn.workerRefcount == 0 {
		workflowCache := cache.New(cacheSize-1, &cache.Options{
			RemovedFunc: func(cachedEntity interface{}) {
				wc := cachedEntity.(*workflowExecutionContextImpl)
				wc.onEviction()
			},
		})
		*storeIn = sharedWorkerCache{workflowCache: workflowCache, maxWorkflowCacheSize: cacheSize}
	}
	storeIn.workerRefcount++
	workerCache := &WorkerCache{
		workflowCache:        storeIn.workflowCache,
		maxWorkflowCacheSize: storeIn.maxWorkflowCacheSize,
	}
	lease := &workerCacheLease{
		sharedCache: storeIn,
		lock:        lock,
	}
	runtime.SetFinalizer(lease, func(lease *workerCacheLease) {
		lease.release()
	})
	return workerCache, lease
}

func (wc *WorkerCache) getWorkflowCache() cache.Cache {
	return wc.workflowCache
}

func (lease *workerCacheLease) release() {
	lease.releaseOnce.Do(func() {
		lease.lock.Lock()
		lease.sharedCache.workerRefcount--
		var releasedCache cache.Cache
		if lease.sharedCache.workerRefcount == 0 {
			releasedCache = lease.sharedCache.workflowCache
			lease.sharedCache.workflowCache = nil
		}
		lease.lock.Unlock()

		if releasedCache != nil {
			releasedCache.Clear()
		}
	})
}

func (wc *WorkerCache) getWorkflowContext(runID string) *workflowExecutionContextImpl {
	o := wc.workflowCache.Get(runID)
	if o == nil {
		return nil
	}
	wec := o.(*workflowExecutionContextImpl)
	return wec
}

func (wc *WorkerCache) putWorkflowContext(runID string, wec *workflowExecutionContextImpl) (*workflowExecutionContextImpl, error) {
	existing, err := wc.workflowCache.PutIfNotExist(runID, wec)
	if err != nil {
		return nil, err
	}
	return existing.(*workflowExecutionContextImpl), nil
}

func (wc *WorkerCache) removeWorkflowContext(runID string) {
	wc.workflowCache.Delete(runID)
}

// MaxWorkflowCacheSize returns the maximum allowed size of the sticky cache
func (wc *WorkerCache) MaxWorkflowCacheSize() int {
	if wc == nil {
		return desiredWorkflowCacheSize
	}
	return wc.maxWorkflowCacheSize
}
