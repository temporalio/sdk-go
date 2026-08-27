package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func LeakWorkflow(ctx workflow.Context, ballastKb int) (string, error) {
	ballast := strings.Repeat("x", ballastKb*1024)
	err := workflow.Await(ctx, func() bool { return len(ballast) == 0 })
	return "done", err
}

func heap() (heapMb float64, goroutines int) {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / 1048576, runtime.NumGoroutine()
}

func main() {
	count := 300
	ballastKb := 1024
	if len(os.Args) > 1 {
		count, _ = strconv.Atoi(os.Args[1])
	}
	if len(os.Args) > 2 {
		ballastKb, _ = strconv.Atoi(os.Args[2])
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7244"})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	w := worker.New(c, "leak-lab-go", worker.Options{})
	w.RegisterWorkflow(LeakWorkflow)
	if err := w.Start(); err != nil {
		log.Fatal(err)
	}
	defer w.Stop()

	ctx := context.Background()
	mb, gr := heap()
	fmt.Printf("baseline: heap %.1f MB, %d goroutines\n", mb, gr)

	for i := 0; i < count; i++ {
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			TaskQueue:                "leak-lab-go",
			WorkflowExecutionTimeout: 10 * time.Minute,
		}, LeakWorkflow, ballastKb)
		if err != nil {
			log.Fatal(err)
		}
		var queryResult string
		v, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "__stack_trace")
		if err == nil {
			_ = v.Get(&queryResult)
		}
		if err := c.TerminateWorkflow(ctx, run.GetID(), run.GetRunID(), "leak-lab"); err != nil {
			log.Fatal(err)
		}
	}
	time.Sleep(2 * time.Second)

	mb, gr = heap()
	fmt.Printf("after %d terminated (%dKB ballast): heap %.1f MB, %d goroutines\n", count, ballastKb, mb, gr)
}
