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
