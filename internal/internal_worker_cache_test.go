package internal

import (
	"sync"
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
	cachePtr := &sharedWorkerCache{}
	var lock sync.Mutex

	cache := newWorkerCache(cachePtr, &lock, 10)
	s.NotNil(cache)
	s.NotNil(cachePtr)
	s.NotNil(cachePtr.workflowCache)
	s.Equal(cachePtr.workerRefcount, 1)
	cache2 := newWorkerCache(cachePtr, &lock, 10)
	s.NotNil(cache2)
	s.NotNil(cachePtr.workflowCache)
	s.Equal(cachePtr.workerRefcount, 2)
	cache.close(&lock)
	s.Equal(cachePtr.workerRefcount, 1)
	s.NotNil(cachePtr.workflowCache)
	cache2.close(&lock)
	s.Equal(cachePtr.workerRefcount, 0)
	s.Nil(cachePtr.workflowCache)
}
