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
