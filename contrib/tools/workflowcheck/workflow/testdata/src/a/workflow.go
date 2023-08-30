package a //want package:"\\d+ non-deterministic vars/funcs"

import (
	"context"
	"text/template"
	"time"

	"go.temporal.io/sdk/workflow"
)

func WorkflowNop(ctx workflow.Context) error {
	return nil
}

func WorkflowCallTime(ctx workflow.Context) error { // want "a.WorkflowCallTime is non-deterministic, reason: calls non-deterministic function time.Now"
	time.Now()
	return nil
}

func WorkflowCallTimeTransitively(ctx workflow.Context) error { // want "a.WorkflowCallTimeTransitively is non-deterministic, reason: calls non-deterministic function a.SomeTimeCall"
	SomeTimeCall()
	return nil
}

func SomeTimeCall() time.Time {
	return time.Now()
}

func WorkflowIterateMap(ctx workflow.Context) error { // want "a.WorkflowIterateMap is non-deterministic, reason: iterates over map"
	var m map[string]string
	for range m {
	}
	return nil
}

func WorkflowWithTemplate(ctx workflow.Context) error { // want "a.WorkflowWithTemplate is non-deterministic, reason: calls non-deterministic function \\(\\*text/template\\.Template\\)\\.Execute.*"
	return template.New("mytmpl").Execute(nil, nil)
}

func WorkflowWithAwait(ctx workflow.Context) error {
	// We do not expect this to fail
	_, err := workflow.AwaitWithTimeout(ctx, 5*time.Second, func() bool { return true })
	return err
}

func WorkflowWithUnnamedArgument(workflow.Context) error { // want "a.WorkflowWithUnnamedArgument is non-deterministic, reason: calls non-deterministic function time.Now"
	time.Now()
	return nil
}

func NotWorkflow(ctx context.Context) {
	time.Now()
}
