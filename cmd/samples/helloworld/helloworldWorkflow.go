package main

import (
	"context"

	"code.uber.internal/devexp/minions-client-go.git/client/cadence"
	"code.uber.internal/devexp/minions-client-go.git/cmd/samples"
	"github.com/Sirupsen/logrus"
)

type (
	helloworldActivity struct{}
	helloworldWorkflow struct {
		helper *samples.SampleHelper
	}
)

/**
* Activities need to implement interface cadence.Activity.
 */
func (a helloworldActivity) Execute(context context.Context, input []byte) ([]byte, error) {
	name := "World"
	if input != nil && len(input) > 0 {
		name = string(input)
	}
	return []byte("Hello " + name + "!"), nil
}

func (a helloworldActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: helloworldActivityName}
}

/**
* Workflow need to implement interface cadence.Workflow
 */
func (w helloworldWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	helloworldResult, err := cadence.ExecuteActivity(ctx, samples.ActivityParameters(helloworldActivityTaskList, helloworldActivityName, input))
	if err != nil {
		logrus.Panicf("Error: %s\n", err.Error())
		return nil, err
	}

	logrus.Info("Workflow completed with result: ", string(helloworldResult))

	w.helper.ResultCh <- nil

	return nil, nil
}
