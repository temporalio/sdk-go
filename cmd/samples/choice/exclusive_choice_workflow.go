package choice

import (
	"context"
	"math/rand"

	"code.uber.internal/devexp/cadence-client-go.git/client/cadence"
	"code.uber.internal/devexp/cadence-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

/**
 * This sample workflow Execute one of many code paths based on the result of an activity.
 */

const (
	sampleWorkflowName      = "sample_choice_workflow"
	sampleTaskList          = "sample_choice_tasklist"
	getOrderActivityName    = "sample_choice_getOrderActivity"
	orderAppleActivityName  = "sample_choice_getNameActivity"
	orderBananaActivityName = "sample_choice_orderBananaActivity"
	orderCherryActivityName = "sample_choice_orderCherryActivity"
	orderOrangeActivityName = "sample_choice_orderOrangeActivity"
)

type (
	// Workflow Deciders and Activities.
	sampleWorkflow      struct{}
	getOrderActivity    struct{}
	orderAppleActivity  struct{}
	orderBananaActivity struct{}
	orderCherryActivity struct{}
	orderOrangeActivity struct{}
)

const (
	orderChoiceApple  = "apple"
	orderChoiceBanana = "banana"
	orderChoiceCherry = "cherry"
	orderChoiceOrange = "orange"
)

var _orderChoices = []string{orderChoiceApple, orderChoiceBanana, orderChoiceCherry, orderChoiceOrange}

// WorkflowConfig specify configuration for sample workflow
var WorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: sampleWorkflowName,
	TaskList:     sampleTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return sampleWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&getOrderActivity{}, &orderAppleActivity{}, &orderBananaActivity{}, &orderCherryActivity{}, &orderOrangeActivity{}},
	WorkflowInput: nil,
}

// Workflow Decider.
func (w sampleWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	// Get order.
	orderResult, _ := cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, getOrderActivityName, nil))

	// choose next activity based on order result
	nextActivityName := getChoiceActivityName(string(orderResult))
	if nextActivityName != "" {
		// execute next activity based on order choice
		cadence.ExecuteActivity(ctx, common.ActivityParameters(sampleTaskList, nextActivityName, nil))
	}

	logrus.Info("Workflow completed.")
	return nil, nil
}

func getChoiceActivityName(orderChoice string) string {
	var nextActivityName string
	switch string(orderChoice) {
	case orderChoiceApple:
		nextActivityName = orderAppleActivityName
	case orderChoiceBanana:
		nextActivityName = orderBananaActivityName
	case orderChoiceCherry:
		nextActivityName = orderCherryActivityName
	case orderChoiceOrange:
		nextActivityName = orderOrangeActivityName
	default:
		logrus.Panicf("Unexpected order %s.", string(orderChoice))
	}
	return nextActivityName
}

func (g getOrderActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	idx := rand.Intn(len(_orderChoices))
	order := _orderChoices[idx]
	logrus.Infof("Order is for %s.", order)
	return []byte(order), nil
}

func (g getOrderActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: getOrderActivityName}
}

func (a orderAppleActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	logrus.Infof("Processed order for %s.", orderChoiceApple)
	return nil, nil
}

func (a orderAppleActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: orderAppleActivityName}
}

func (a orderBananaActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	logrus.Infof("Processed order for %s.", orderChoiceBanana)
	return nil, nil
}

func (a orderBananaActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: orderBananaActivityName}
}

func (a orderCherryActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	logrus.Infof("Processed order for %s.", orderChoiceCherry)
	return nil, nil
}

func (a orderCherryActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: orderCherryActivityName}
}

func (a orderOrangeActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	logrus.Infof("Processed order for %s.", orderChoiceOrange)
	return nil, nil
}

func (a orderOrangeActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: orderOrangeActivityName}
}
