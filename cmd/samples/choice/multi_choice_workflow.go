package choice

import (
	"context"
	"encoding/json"
	"math/rand"

	"code.uber.internal/devexp/cadence-client-go.git/client/cadence"
	"code.uber.internal/devexp/cadence-client-go.git/cmd/samples/common"
	"github.com/Sirupsen/logrus"
)

/**
 * This multi choice sample workflow Execute different parallel branches based on the result of an activity.
 */
const (
	multiChoiceWorkflowName    = "sample_multi_choice_workflow"
	multiChoiceTaskList        = "sample_multi_choice_tasklist"
	getBasketOrderActivityName = "sample_multi_choice_getBusketOrderActivity"
)

type (
	// Workflow Deciders and Activities.
	multiChoiceWorkflow    struct{}
	getBasketOrderActivity struct{}
)

// MultiChoiceWorkflowConfig specify configuration for multiChoiceWorkflow
var MultiChoiceWorkflowConfig = common.SampleWorkflowConfig{
	WorkflowName: multiChoiceWorkflowName,
	TaskList:     multiChoiceTaskList,
	WorkflowFactory: func(wt cadence.WorkflowType) (cadence.Workflow, error) {
		return multiChoiceWorkflow{}, nil
	},
	Activities:    []cadence.Activity{&getBasketOrderActivity{}, &orderAppleActivity{}, &orderBananaActivity{}, &orderCherryActivity{}, &orderOrangeActivity{}},
	WorkflowInput: nil,
}

// Workflow Decider.
func (w multiChoiceWorkflow) Execute(ctx cadence.Context, input []byte) (result []byte, err error) {
	// Get basket order.
	basketResult, _ := cadence.ExecuteActivity(ctx, common.ActivityParameters(multiChoiceTaskList, getBasketOrderActivityName, nil))

	choices := make([]string, 0)
	err = json.Unmarshal(basketResult, &choices)
	if err != nil {
		logrus.Panicf("Unmarshal failed %+v", err)
		return nil, err
	}

	var futures []cadence.Future
	for _, item := range choices {
		// choose next activity based on order result
		nextActivityName := getChoiceActivityName(item)
		if nextActivityName != "" {
			// execute next activity based on order choice
			future := cadence.ExecuteActivityAsync(ctx, common.ActivityParameters(multiChoiceTaskList, nextActivityName, nil))
			futures = append(futures, future)
		}
	}

	// wait until all items in the basket order are processed
	for _, future := range futures {
		future.Get(ctx)
	}

	logrus.Info("Workflow completed.")
	return nil, nil
}

func (g getBasketOrderActivity) Execute(ctx context.Context, input []byte) ([]byte, error) {
	var basket []string
	for _, item := range _orderChoices {
		// some random decision
		if rand.Float32() <= 0.65 {
			basket = append(basket, item)
		}
	}

	if len(basket) == 0 {
		basket = append(basket, _orderChoices[rand.Intn(len(_orderChoices))])
	}

	logrus.Infof("Basket order includes %s.", basket)
	return json.Marshal(basket)
}

func (g getBasketOrderActivity) ActivityType() cadence.ActivityType {
	return cadence.ActivityType{Name: getBasketOrderActivityName}
}
