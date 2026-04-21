package a //want package:"\\d+ non-deterministic vars/funcs"

import (
	"context"
	"text/template"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func WorkflowNop(ctx workflow.Context) error {
	return nil
}

func WorkflowCallTime(ctx workflow.Context) error { // want "a.WorkflowCallTime is non-deterministic, reason: calls non-deterministic function time.Now"
	time.Now()
	return nil
}

func WorkflowCallTimeInSideEffect(ctx workflow.Context) error {
	workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return time.Now()
	})
	return nil
}

func WorkflowCallTimeInSideEffectAndNot(ctx workflow.Context) error { // want "a.WorkflowCallTimeInSideEffectAndNot is non-deterministic, reason: calls non-deterministic function time.Now"
	workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return time.Now()
	})
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

// --- Anonymous function in ExecuteLocalActivity tests ---

func WorkflowLocalActivityLiteral(ctx workflow.Context) error { // want "a.WorkflowLocalActivityLiteral is non-deterministic, reason: anonymous function passed to ExecuteLocalActivity that has a non-deterministic function name"
	workflow.ExecuteLocalActivity(ctx, func(ctx context.Context) error { return nil })
	return nil
}

func WorkflowLocalActivityVarLiteral(ctx workflow.Context) error { // want "a.WorkflowLocalActivityVarLiteral is non-deterministic, reason: anonymous function passed to ExecuteLocalActivity that has a non-deterministic function name"
	f := func(ctx context.Context) error { return nil }
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityVarReassignedToNamed(ctx workflow.Context) error {
	f := func(ctx context.Context) error { return nil }
	f = myLocalActivity
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityVarReassignedToAnon(ctx workflow.Context) error { // want "a.WorkflowLocalActivityVarReassignedToAnon is non-deterministic, reason: anonymous function passed to ExecuteLocalActivity that has a non-deterministic function name"
	f := myLocalActivity
	f = func(ctx context.Context) error { return nil }
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityBranchAnon(ctx workflow.Context) error { // want "a.WorkflowLocalActivityBranchAnon is non-deterministic, reason: anonymous function passed to ExecuteLocalActivity that has a non-deterministic function name"
	f := myLocalActivity
	if true {
		f = func(ctx context.Context) error { return nil }
	}
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityIfElseAnon(ctx workflow.Context) error { // want "a.WorkflowLocalActivityIfElseAnon is non-deterministic, reason: anonymous function passed to ExecuteLocalActivity that has a non-deterministic function name"
	var f func(ctx context.Context) error
	if true {
		f = func(ctx context.Context) error { return nil }
	} else {
		f = myLocalActivity
	}
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityIfElseAllNamed(ctx workflow.Context) error {
	var f func(ctx context.Context) error
	if true {
		f = myLocalActivity
	} else {
		f = myLocalActivity
	}
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityIfElseOverridden(ctx workflow.Context) error {
	var f func(ctx context.Context) error
	if true {
		f = func(ctx context.Context) error { return nil }
	} else {
		f = myLocalActivity
	}
	f = myLocalActivity
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityAnonInClosure(ctx workflow.Context) error {
	f := myLocalActivity
	_ = func() {
		f = func(ctx context.Context) error { return nil }
		_ = f
	}
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityFuncParam(ctx workflow.Context, f func(ctx context.Context) error) error {
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityBranchOverridden(ctx workflow.Context) error {
	if true {
		f := func(ctx context.Context) error { return nil }
		_ = f
	}
	// f here is a different variable; this uses myLocalActivity directly
	workflow.ExecuteLocalActivity(ctx, myLocalActivity)
	return nil
}

func myLocalActivity(ctx context.Context) error { return nil }

func WorkflowLocalActivityNamed(ctx workflow.Context) error {
	workflow.ExecuteLocalActivity(ctx, myLocalActivity)
	return nil
}

type ActivityStruct struct{}

func (a *ActivityStruct) Run(ctx context.Context) error { return nil }

func WorkflowLocalActivityMethodValue(ctx workflow.Context) error {
	a := &ActivityStruct{}
	workflow.ExecuteLocalActivity(ctx, a.Run)
	return nil
}

func WorkflowLocalActivityString(ctx workflow.Context) error {
	workflow.ExecuteLocalActivity(ctx, "MyActivity")
	return nil
}

func WorkflowLocalActivityMethodExpr(ctx workflow.Context) error {
	workflow.ExecuteLocalActivity(ctx, (*ActivityStruct).Run)
	return nil
}

func WorkflowLocalActivityNamedVar(ctx workflow.Context) error {
	f := myLocalActivity
	workflow.ExecuteLocalActivity(ctx, f)
	return nil
}

func WorkflowLocalActivityIgnored(ctx workflow.Context) error {
	workflow.ExecuteLocalActivity(ctx, func(ctx context.Context) error { return nil }) //workflowcheck:ignore
	return nil
}

func WorkflowLocalActivityAssignIgnored(ctx workflow.Context) error {
	_ = workflow.ExecuteLocalActivity(ctx, func(ctx context.Context) error { return nil }) //workflowcheck:ignore
	return nil
}

func WorkflowWithSearchAttributes(workflow.Context) {
	sa := temporal.SearchAttributes{}
	_ = sa.Copy()
}
