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
