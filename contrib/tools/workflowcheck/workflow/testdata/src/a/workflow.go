package a

import (
	"time"

	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func PrepWorkflow() {
	var wrk worker.Worker
	wrk.RegisterWorkflow(WorkflowNop)
	wrk.RegisterWorkflow(WorkflowCallTime)             // want "a.WorkflowCallTime is non-deterministic, reason: calls non-deterministic function time.Now"
	wrk.RegisterWorkflow(WorkflowCallTimeTransitively) // want "a.WorkflowCallTimeTransitively is non-deterministic, reason: calls non-deterministic function a.SomeTimeCall"
	wrk.RegisterWorkflow(WorkflowIterateMap)           // want "a.WorkflowIterateMap is non-deterministic, reason: iterates over map"
}

func WorkflowNop(ctx workflow.Context) error {
	return nil
}

func WorkflowCallTime(ctx workflow.Context) error { // want WorkflowCallTime:"calls non-deterministic function time.Now"
	time.Now()
	return nil
}

func WorkflowCallTimeTransitively(ctx workflow.Context) error { // want WorkflowCallTimeTransitively:"calls non-deterministic function a.SomeTimeCall"
	SomeTimeCall()
	return nil
}

func SomeTimeCall() time.Time { // want SomeTimeCall:"calls non-deterministic function time.Now"
	return time.Now()
}

func WorkflowIterateMap(ctx workflow.Context) error { // want WorkflowIterateMap:"iterates over map"
	var m map[string]string
	for range m {
	}
	return nil
}
