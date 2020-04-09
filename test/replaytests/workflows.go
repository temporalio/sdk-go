package replaytests

import (
	"context"
	"time"

	"go.uber.org/zap"

	"go.temporal.io/temporal/activity"
	"go.temporal.io/temporal/workflow"
)

// ApplicationName is the task list for this sample
const ApplicationName = "helloWorldGroup"

// Workflow workflow decider
func Workflow(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string
	v := workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)
	if v == workflow.DefaultVersion {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", zap.Error(err))
			return err
		}
	} else {
		err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", zap.Error(err))
			return err
		}

		err = workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
		if err != nil {
			logger.Error("Activity failed.", zap.Error(err))
			return err
		}
	}

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", zap.Error(err))
		return err
	}

	logger.Info("Workflow completed.", zap.String("Result", helloworldResult))

	return nil
}

// Workflow2 workflow decider
func Workflow2(ctx workflow.Context, name string) error {
	ao := workflow.ActivityOptions{
		ScheduleToStartTimeout: time.Minute,
		StartToCloseTimeout:    time.Minute,
		HeartbeatTimeout:       time.Second * 20,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	logger := workflow.GetLogger(ctx)
	logger.Info("helloworld workflow started")
	var helloworldResult string

	workflow.GetVersion(ctx, "test-change", workflow.DefaultVersion, 1)

	_ = workflow.UpsertSearchAttributes(ctx, map[string]interface{}{"CustomKeywordField": "testkey"})

	err := workflow.ExecuteActivity(ctx, helloworldActivity, name).Get(ctx, &helloworldResult)
	if err != nil {
		logger.Error("Activity failed.", zap.Error(err))
		return err
	}

	logger.Info("Workflow completed.", zap.String("Result", helloworldResult))

	return nil
}

func helloworldActivity(ctx context.Context, name string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("helloworld activity started")
	return "Hello " + name + "!", nil
}
