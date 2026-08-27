package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var wakeups atomic.Int64

func ProbeWorkflow(ctx workflow.Context) (string, error) {
	err := workflow.Await(ctx, func() bool {
		wakeups.Add(1)
		return false
	})
	return "done", err
}

func main() {
	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7244"})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	w := worker.New(c, "probe-go", worker.Options{})
	w.RegisterWorkflow(ProbeWorkflow)
	if err := w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	ctx := context.Background()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:                "probe-go",
		WorkflowExecutionTimeout: 10 * time.Minute,
	}, ProbeWorkflow)
	if err != nil {
		log.Fatal(err)
	}

	var pong string
	v, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "__stack_trace")
	if err != nil {
		log.Fatal(err)
	}
	_ = v.Get(&pong)

	beforeWakeups := wakeups.Load()
	beforeGoroutines := runtime.NumGoroutine()
	fmt.Printf("before terminate: %d predicate wakeups, %d goroutines\n", beforeWakeups, beforeGoroutines)

	if err := c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "probe"); err != nil {
		log.Fatal(err)
	}
	fmt.Println("terminated; waiting 10s for anything to reach the worker...")
	time.Sleep(10 * time.Second)

	fmt.Printf("after terminate: %d predicate wakeups (delta %d), %d goroutines (delta %d)\n",
		wakeups.Load(), wakeups.Load()-beforeWakeups,
		runtime.NumGoroutine(), runtime.NumGoroutine()-beforeGoroutines)

	fmt.Println("server-side history of the terminated run:")
	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		ev, err := iter.Next()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %s\n", ev.GetEventType())
	}
}
