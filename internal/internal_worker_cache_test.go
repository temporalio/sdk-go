package internal

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/internal/common/metrics"
)

type (
	WorkerCacheSuite struct {
		suite.Suite
	}
)

func TestWorkerCacheTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerCacheSuite))
}

func newTestWorkerCache(t testing.TB) *WorkerCache {
	t.Helper()
	workerCache, lease := NewWorkerCache()
	t.Cleanup(lease.release)
	return workerCache
}

func (s *WorkerCacheSuite) TestCreateAndFree() {
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex

	cache, lease := newWorkerCache(cachePtr, &lock, 10)
	s.NotNil(cache)
	s.NotNil(cachePtr)
	s.NotNil(cachePtr.workflowCache)
	s.Equal(cachePtr.workerRefcount, 1)
	cache2, lease2 := newWorkerCache(cachePtr, &lock, 10)
	s.NotNil(cache2)
	s.NotNil(cachePtr.workflowCache)
	s.Equal(cachePtr.workerRefcount, 2)
	workflowContext := &workflowExecutionContextImpl{
		wth: &workflowTaskHandlerImpl{metricsHandler: metrics.NopHandler},
	}
	_, err := cache.putWorkflowContext("run-id", workflowContext)
	s.NoError(err)
	lease.release()
	s.Equal(cachePtr.workerRefcount, 1)
	s.NotNil(cachePtr.workflowCache)
	s.Equal(1, cache.getWorkflowCache().Size())
	lease2.release()
	s.Equal(cachePtr.workerRefcount, 0)
	s.Nil(cachePtr.workflowCache)
	s.Zero(cache.getWorkflowCache().Size())

	lease2.release()
	s.Equal(0, cachePtr.workerRefcount)

	cache3, lease3 := newWorkerCache(cachePtr, &lock, 10)
	s.NotSame(cache.getWorkflowCache(), cache3.getWorkflowCache())
	lease3.release()
}

func (s *WorkerCacheSuite) TestFinalReleaseClearsWithoutMetric() {
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex
	cache, lease := newWorkerCache(cachePtr, &lock, 10)
	metricsHandler := metrics.NewCapturingHandler()
	workflowContext := &workflowExecutionContextImpl{
		previousStartedEventID: 1,
		wth: &workflowTaskHandlerImpl{
			metricsHandler: metricsHandler,
		},
	}
	_, err := cache.putWorkflowContext("run-id", workflowContext)
	s.NoError(err)

	lease.release()

	s.Zero(cache.getWorkflowCache().Size())
	s.Nil(cachePtr.workflowCache)
	s.Eventually(func() bool {
		workflowContext.mutex.Lock()
		defer workflowContext.mutex.Unlock()

		return workflowContext.previousStartedEventID == 0
	}, time.Second, time.Millisecond)
	s.Empty(metricsHandler.Counters())
}

func (s *WorkerCacheSuite) TestExplicitClearCountsForcedEviction() {
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex
	cache, lease := newWorkerCache(cachePtr, &lock, 10)
	defer lease.release()
	metricsHandler := metrics.NewCapturingHandler()
	workflowContext := &workflowExecutionContextImpl{
		wth: &workflowTaskHandlerImpl{
			metricsHandler: metricsHandler,
		},
	}
	_, err := cache.putWorkflowContext("run-id", workflowContext)
	s.NoError(err)

	cache.getWorkflowCache().Clear()

	s.Eventually(func() bool {
		counters := metricsHandler.Counters()
		return len(counters) == 1 && counters[0].Value() == 1
	}, time.Second, time.Millisecond)
	s.Equal(metrics.StickyCacheTotalForcedEviction, metricsHandler.Counters()[0].Name)
}

func (s *WorkerCacheSuite) TestOldHandleCannotAffectLaterGeneration() {
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex

	oldCache, oldLease := newWorkerCache(cachePtr, &lock, 10)
	oldLease.release()
	newCache, newLease := newWorkerCache(cachePtr, &lock, 10)
	defer newLease.release()

	workflowContext := &workflowExecutionContextImpl{
		wth: &workflowTaskHandlerImpl{metricsHandler: metrics.NopHandler},
	}
	_, err := newCache.putWorkflowContext("run-id", workflowContext)
	s.NoError(err)

	oldCache.removeWorkflowContext("run-id")
	s.Same(workflowContext, newCache.getWorkflowContext("run-id"))
	s.NotSame(oldCache.getWorkflowCache(), newCache.getWorkflowCache())
}

func (s *WorkerCacheSuite) TestConcurrentReleaseIsIdempotent() {
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex
	_, lease := newWorkerCache(cachePtr, &lock, 10)

	var released sync.WaitGroup
	for range 100 {
		released.Add(1)
		go func() {
			defer released.Done()
			lease.release()
		}()
	}
	released.Wait()

	s.Zero(cachePtr.workerRefcount)
	s.Nil(cachePtr.workflowCache)
}
