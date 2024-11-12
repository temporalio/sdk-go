// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	failurepb "go.temporal.io/api/failure/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSetMemoOnStart(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	memo := map[string]interface{}{
		"key": make(chan int),
	}
	err := env.SetMemoOnStart(memo)
	require.Error(t, err)

	memo = map[string]interface{}{
		"memoKey": "memo",
	}
	require.Nil(t, env.impl.workflowInfo.Memo)
	err = env.SetMemoOnStart(memo)
	require.NoError(t, err)
	require.NotNil(t, env.impl.workflowInfo.Memo)
}

func TestSetSearchAttributesOnStart(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	invalidSearchAttr := map[string]interface{}{
		"key": make(chan int),
	}
	err := env.SetSearchAttributesOnStart(invalidSearchAttr)
	require.Error(t, err)

	searchAttr := map[string]interface{}{
		"CustomIntField": 1,
	}
	err = env.SetSearchAttributesOnStart(searchAttr)
	require.NoError(t, err)
	require.NotNil(t, env.impl.workflowInfo.SearchAttributes)
}

func TestSetTypedSearchAttributesOnStart(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	CustomIntKey := NewSearchAttributeKeyInt64("CustomIntField")

	err := env.SetTypedSearchAttributesOnStart(NewSearchAttributes(CustomIntKey.ValueSet(1)))
	require.NoError(t, err)
	require.NotNil(t, env.impl.workflowInfo.SearchAttributes)
}

func TestNoExplicitRegistrationRequired(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	activity := func(ctx context.Context, arg string) (string, error) { return arg + " World!", nil }
	env.RegisterActivity(activity)
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
			ScheduleToStartTimeout: time.Hour,
		})
		var result string
		err := ExecuteActivity(ctx, activity, arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestUnregisteredActivity(t *testing.T) {
	t.Parallel()
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.RegisterActivity(helloWorldAct)
	workflow := func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			ScheduleToStartTimeout: time.Minute,
			StartToCloseTimeout:    time.Minute,
		})
		return ExecuteActivity(ctx, "unregistered").Get(ctx, nil)
	}
	env.RegisterWorkflow(workflow)
	env.ExecuteWorkflow(workflow)
	require.Error(t, env.GetWorkflowError())
	err := env.GetWorkflowError()
	require.Error(t, err)
	var workflowErr *WorkflowExecutionError
	require.True(t, errors.As(err, &workflowErr))

	err = errors.Unwrap(workflowErr)
	var activityErr *ActivityError
	require.True(t, errors.As(err, &activityErr))

	err = errors.Unwrap(activityErr)
	var err1 *ApplicationError
	require.True(t, errors.As(err, &err1))

	require.True(t, strings.HasPrefix(err1.Error(), "unable to find activityType=unregistered"), err1.Error())
}

func namedActivity(ctx context.Context, arg string) (string, error) {
	return arg + " World!", nil
}

func TestLocalActivityExecutionByActivityName(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestLocalActivityExecutionByActivityNameAlias(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(namedActivity, RegisterActivityOptions{
		Name: "localActivity",
	})
	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "localActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello World!", result)
}

func TestLocalActivityExecutionByActivityNameAliasMissingRegistration(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "localActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")
	require.NotNil(t, env.GetWorkflowError())
}

func TestWorkflowIDInsideTestWorkflow(t *testing.T) {
	var suite WorkflowTestSuite
	// Default ID
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: "+defaultTestWorkflowID, str)

	// Custom ID
	env = suite.NewTestWorkflowEnvironment()
	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: my-workflow-id", str)
}

func TestWorkflowIDSignalWorkflowByID(t *testing.T) {
	var suite WorkflowTestSuite
	// Test SignalWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		err := env.SignalWorkflowByID("my-workflow-id", "signal", "payload")
		require.NoError(t, err)
	}, time.Second)

	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		var result string
		GetSignalChannel(ctx, "signal").Receive(ctx, &result)
		return "id is: " + GetWorkflowInfo(ctx).WorkflowExecution.ID, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "id is: my-workflow-id", str)
}

func TestWorkflowIDUpdateWorkflowByID(t *testing.T) {
	var suite WorkflowTestSuite
	// Test UpdateWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		err := env.UpdateWorkflowByID("my-workflow-id", "update", "id", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		}, "input")
		require.NoError(t, err)
	}, time.Second)

	env.SetStartWorkflowOptions(StartWorkflowOptions{ID: "my-workflow-id"})
	env.ExecuteWorkflow(func(ctx Context) (string, error) {
		var result string
		err := SetUpdateHandler(ctx, "update", func(ctx Context, input string) error {
			result = input
			return nil
		}, UpdateHandlerOptions{})
		if err != nil {
			return "", err
		}
		err = Await(ctx, func() bool { return result != "" })
		return result, err
	})
	require.NoError(t, env.GetWorkflowError())
	var str string
	require.NoError(t, env.GetWorkflowResult(&str))
	require.Equal(t, "input", str)
}

func TestChildWorkflowUpdate(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	wfID := "fake_workflow_id"
	env.SetStartWorkflowOptions(StartWorkflowOptions{
		ID: wfID,
	})
	env.RegisterDelayedCallback(func() {
		err := env.UpdateWorkflowByID("child-workflow", "child-handler", "1", &updateCallback{
			accept: func() {
			},
			reject: func(err error) {
				require.Fail(t, "update failed", err)
			},
			complete: func(result interface{}, err error) {
				if err != nil {
					require.Fail(t, "update failed", err)
				}
			},
		}, nil)
		assert.NoError(t, err)
	}, time.Second*5)

	env.RegisterWorkflow(updateChildWf)
	env.RegisterWorkflow(updateParentWf)

	env.ExecuteWorkflow(updateParentWf)
	assert.NoError(t, env.GetWorkflowErrorByID(wfID))
}

func updateParentWf(ctx Context) error {
	if err := SetUpdateHandler(ctx, "parent-handler", func(ctx Context, input interface{}) error {
		return nil
	}, UpdateHandlerOptions{}); err != nil {
		return err
	}

	var childErr error
	if err := ExecuteChildWorkflow(WithChildWorkflowOptions(ctx, ChildWorkflowOptions{
		WorkflowID: "child-workflow",
	}), updateChildWf).Get(ctx, &childErr); err != nil {
		return err
	}
	return childErr
}

func updateChildWf(ctx Context) error {
	var done bool
	err := SetUpdateHandler(ctx, "child-handler", func(ctx Context, input interface{}) error {
		done = true
		return nil
	}, UpdateHandlerOptions{})
	if err != nil {
		return err
	}

	return Await(ctx, func() bool {
		return done
	})
}

func TestWorkflowUpdateOrder(t *testing.T) {
	var suite WorkflowTestSuite
	// Test UpdateWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.ExecuteWorkflow(func(ctx Context) (int, error) {
		var inflightUpdates int
		var ranUpdates int
		err := SetUpdateHandler(ctx, "update", func(ctx Context) error {
			inflightUpdates++
			ranUpdates++
			defer func() {
				inflightUpdates--
			}()
			return Sleep(ctx, time.Hour)
		}, UpdateHandlerOptions{})
		if err != nil {
			return 0, err
		}
		err = Await(ctx, func() bool { return inflightUpdates == 0 })
		return ranUpdates, err
	})
	require.NoError(t, env.GetWorkflowError())
	var result int
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, 1, result)
}

func TestWorkflowNotRegisteredRejected(t *testing.T) {
	var suite WorkflowTestSuite
	// Test UpdateWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	var updateRejectionErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id", &updateCallback{
			reject: func(err error) {
				updateRejectionErr = err
			},
			accept: func() {
				require.Fail(t, "update should not be accepted")
			},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.ExecuteWorkflow(func(ctx Context) error {
		return Sleep(ctx, time.Hour)
	})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, env.GetWorkflowResult(nil))
	require.Error(t, updateRejectionErr)
	require.Equal(t, "unknown update update. KnownUpdates=[]", updateRejectionErr.Error())
}

func TestWorkflowUpdateOrderAcceptReject(t *testing.T) {
	var suite WorkflowTestSuite
	// Test UpdateWorkflowByID works with custom ID
	env := suite.NewTestWorkflowEnvironment()
	// Send 3 updates, with one bad update
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "1", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, 0)

	var updateRejectionErr error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("bad update", "2", &updateCallback{
			reject: func(err error) {
				updateRejectionErr = err
			},
			accept: func() {
				require.Fail(t, "update should not be rejected")
			},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "3", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.ExecuteWorkflow(func(ctx Context) (int, error) {
		var inflightUpdates int
		var ranUpdates int
		err := SetUpdateHandler(ctx, "update", func(ctx Context) error {
			inflightUpdates++
			ranUpdates++
			defer func() {
				inflightUpdates--
			}()
			return Sleep(ctx, time.Hour)
		}, UpdateHandlerOptions{})
		if err != nil {
			return 0, err
		}
		err = Await(ctx, func() bool { return inflightUpdates == 0 })
		return ranUpdates, err
	})
	require.NoError(t, env.GetWorkflowError())
	var result int
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, 2, result)

	require.Error(t, updateRejectionErr)
	require.Equal(t, "unknown update bad update. KnownUpdates=[update]", updateRejectionErr.Error())
}

func TestWorkflowDuplicateIDDedup(t *testing.T) {
	var suite WorkflowTestSuite
	// Test dev server dedups UpdateWorkflow with same ID
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id", &updateCallback{
			reject: func(err error) {
				require.Fail(t, fmt.Sprintf("update should not be rejected, err: %v", err))
			},
			accept: func() {
			},
			complete: func(result interface{}, err error) {
				intResult, ok := result.(int)
				if !ok {
					require.Fail(t, "result should be int")
				} else {
					require.Equal(t, 0, intResult)
				}
			},
		}, 0)
	}, 0)

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id", &updateCallback{
			reject: func(err error) {
				require.Fail(t, fmt.Sprintf("update should not be rejected, err: %v", err))
			},
			accept: func() {
			},
			complete: func(result interface{}, err error) {
				intResult, ok := result.(int)
				if !ok {
					require.Fail(t, "result should be int")
				} else {
					// if dedup, this be okay, even if we pass in 1 as arg, since it's deduping,
					// the result should match the first update's result, 0
					require.Equal(t, 0, intResult)
				}
			},
		}, 1)

	}, 1*time.Millisecond)

	env.ExecuteWorkflow(func(ctx Context) error {
		err := SetUpdateHandler(ctx, "update", func(ctx Context, i int) (int, error) {
			return i, nil
		}, UpdateHandlerOptions{})
		if err != nil {
			return err
		}
		return Sleep(ctx, time.Hour)
	})
	require.NoError(t, env.GetWorkflowError())
}

func TestAllHandlersFinished(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id_1", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("update", "id_2", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, time.Minute)

	env.ExecuteWorkflow(func(ctx Context) (int, error) {
		var inflightUpdates int
		var ranUpdates int
		err := SetUpdateHandler(ctx, "update", func(ctx Context) error {
			inflightUpdates++
			ranUpdates++
			defer func() {
				inflightUpdates--
			}()
			return Sleep(ctx, time.Hour)
		}, UpdateHandlerOptions{
			Validator: func() error {
				if AllHandlersFinished(ctx) {
					return errors.New("AllHandlersFinished should return false in a validator")
				}
				return nil
			},
		})
		if err != nil {
			return 0, err
		}
		err = Await(ctx, func() bool { return AllHandlersFinished(ctx) })
		return ranUpdates, err
	})
	require.NoError(t, env.GetWorkflowError())
	var result int
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, 2, result)
}

// parseLogs parses the logs from the buffer and returns the logs as a slice of maps
func parseLogs(t *testing.T, buf *bytes.Buffer) []map[string]any {
	var ms []map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		err := json.Unmarshal(line, &m)
		require.NoError(t, err)
		fmt.Println(m)
		ms = append(ms, m)
	}
	return ms
}

func TestWorkflowAllHandlersFinished(t *testing.T) {
	// runWf runs a workflow that sends two updates and then signals the workflow to complete
	runWf := func(completionType string, buf *bytes.Buffer) (int, error) {
		var suite WorkflowTestSuite
		th := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})
		suite.SetLogger(log.NewStructuredLogger(slog.New(th)))
		env := suite.NewTestWorkflowEnvironment()

		env.RegisterDelayedCallback(func() {
			env.UpdateWorkflow("update", "id_1", &updateCallback{
				reject: func(err error) {
					require.Fail(t, "update should not be rejected")
				},
				accept:   func() {},
				complete: func(interface{}, error) {},
			})
		}, 0)

		env.RegisterDelayedCallback(func() {
			env.UpdateWorkflow("update", "id_2", &updateCallback{
				reject: func(err error) {
					require.Fail(t, "update should not be rejected")
				},
				accept:   func() {},
				complete: func(interface{}, error) {},
			})
		}, time.Minute)

		env.RegisterDelayedCallback(func() {
			env.UpdateWorkflow("nonWarningHandler", "id_3", &updateCallback{
				reject: func(err error) {
					require.Fail(t, "update should not be rejected")
				},
				accept:   func() {},
				complete: func(interface{}, error) {},
			})
		}, 2*time.Minute)

		env.RegisterDelayedCallback(func() {
			if completionType == "cancel" {
				env.CancelWorkflow()
			} else {
				env.SignalWorkflow("completion", completionType)
			}
		}, time.Minute*2)

		env.ExecuteWorkflow(func(ctx Context) (int, error) {
			var inflightUpdates int
			var ranUpdates int
			err := SetUpdateHandler(ctx, "update", func(ctx Context) error {
				inflightUpdates++
				ranUpdates++
				defer func() {
					inflightUpdates--
				}()
				return Sleep(ctx, time.Hour)
			}, UpdateHandlerOptions{})
			if err != nil {
				return 0, err
			}

			err = SetUpdateHandler(ctx, "nonWarningHandler", func(ctx Context) error {
				inflightUpdates++
				ranUpdates++
				defer func() {
					inflightUpdates--
				}()
				return Sleep(ctx, time.Hour)
			}, UpdateHandlerOptions{
				UnfinishedPolicy: HandlerUnfinishedPolicyAbandon,
			})
			if err != nil {
				return 0, err
			}

			var completeType string
			s := NewSelector(ctx)
			s.AddReceive(ctx.Done(), func(c ReceiveChannel, more bool) {
				completeType = "cancel"
			}).AddReceive(GetSignalChannel(ctx, "completion"), func(c ReceiveChannel, more bool) {
				c.Receive(ctx, &completeType)
			}).Select(ctx)

			if completeType == "cancel" {
				return 0, ctx.Err()
			} else if completeType == "complete" {
				return ranUpdates, nil
			} else if completeType == "failure" {
				return 0, errors.New("test workflow failed")
			} else if completeType == "continue-as-new" {
				return 0, NewContinueAsNewError(ctx, "continue-as-new", nil)
			} else {
				panic("unknown completion type")
			}
		})
		err := env.GetWorkflowError()
		if err != nil {
			return 0, err
		}
		var result int
		require.NoError(t, env.GetWorkflowResult(&result))
		return result, nil
	}
	// parseWarnedUpdates parses the warned updates from the logs and returns them as a slice of maps
	parseWarnedUpdates := func(updates interface{}) []map[string]interface{} {
		var warnedUpdates []map[string]interface{}
		for _, update := range updates.([]interface{}) {
			warnedUpdates = append(warnedUpdates, update.(map[string]interface{}))
		}
		return warnedUpdates

	}
	// assertExpectedLogs asserts that the logs in the buffer are as expected
	assertExpectedLogs := func(t *testing.T, buf *bytes.Buffer, shouldWarn bool) {
		logs := parseLogs(t, buf)
		if shouldWarn {
			require.Len(t, logs, 1)
			require.Equal(t, unhandledUpdateWarningMessage, logs[0]["msg"])
			warnedUpdates := parseWarnedUpdates(logs[0]["Updates"])
			require.Len(t, warnedUpdates, 2)
			// Order of updates is not guaranteed
			require.Equal(t, "update", warnedUpdates[0]["name"])
			require.True(t, warnedUpdates[0]["id"] == "id_1" || warnedUpdates[0]["id"] == "id_2")
			require.Equal(t, "update", warnedUpdates[1]["name"])
			require.True(t, warnedUpdates[1]["id"] != warnedUpdates[0]["id"])
			require.True(t, warnedUpdates[1]["id"] == "id_1" || warnedUpdates[1]["id"] == "id_2")
		} else {
			require.Len(t, logs, 0)
		}
	}

	t.Run("complete", func(t *testing.T) {
		var buf bytes.Buffer
		result, err := runWf("complete", &buf)
		require.NoError(t, err)
		require.Equal(t, 3, result)
		assertExpectedLogs(t, &buf, true)
	})
	t.Run("cancel", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := runWf("cancel", &buf)
		require.Error(t, err)
		assertExpectedLogs(t, &buf, true)
	})
	t.Run("failure", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := runWf("failure", &buf)
		require.Error(t, err)
		assertExpectedLogs(t, &buf, false)
	})
	t.Run("continue-as-new", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := runWf("continue-as-new", &buf)
		require.Error(t, err)
		assertExpectedLogs(t, &buf, true)
	})
}

func TestWorkflowUpdateLogger(t *testing.T) {
	var suite WorkflowTestSuite
	var buf bytes.Buffer
	th := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	suite.SetLogger(log.NewStructuredLogger(slog.New(th)))
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("logging_update", "id_1", &updateCallback{
			reject: func(err error) {
				require.Fail(t, "update should not be rejected")
			},
			accept:   func() {},
			complete: func(interface{}, error) {},
		})
	}, 0)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("completion", nil)
	}, time.Minute*2)

	env.ExecuteWorkflow(func(ctx Context) (int, error) {
		var ranUpdates int
		err := SetUpdateHandler(ctx, "logging_update", func(ctx Context) error {
			ranUpdates++
			log := GetLogger(ctx)
			log.Info("logging update handler")
			return nil
		}, UpdateHandlerOptions{
			Validator: func(ctx Context) error {
				log := GetLogger(ctx)
				log.Info("logging update validator")
				return nil
			},
		})
		if err != nil {
			return 0, err
		}

		var completeType string
		s := NewSelector(ctx)
		s.AddReceive(ctx.Done(), func(c ReceiveChannel, more bool) {
			completeType = "cancel"
		}).AddReceive(GetSignalChannel(ctx, "completion"), func(c ReceiveChannel, more bool) {
			c.Receive(ctx, &completeType)
		}).Select(ctx)
		return ranUpdates, nil
	})

	require.NoError(t, env.GetWorkflowError())
	var result int
	require.NoError(t, env.GetWorkflowResult(&result))
	// Verify logs
	logs := parseLogs(t, &buf)
	require.Len(t, logs, 2)
	require.Equal(t, logs[0][tagUpdateName], "logging_update")
	require.Equal(t, logs[0][tagUpdateID], "id_1")
	require.Equal(t, logs[0]["msg"], "logging update validator")
	require.Equal(t, logs[1][tagUpdateName], "logging_update")
	require.Equal(t, logs[1][tagUpdateID], "id_1")
	require.Equal(t, logs[1]["msg"], "logging update handler")

}

func TestWorkflowStartTimeInsideTestWorkflow(t *testing.T) {
	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx Context) (int64, error) {
		return GetWorkflowInfo(ctx).WorkflowStartTime.Unix(), nil
	})
	require.NoError(t, env.GetWorkflowError())
	var timestamp int64
	require.NoError(t, env.GetWorkflowResult(&timestamp))
	require.Equal(t, env.Now().Unix(), timestamp)
}

func TestActivityAssertCalled(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.OnActivity(namedActivity, mock.Anything, mock.Anything).Return("Mock!", nil)

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		err := ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		if err != nil {
			return "", err
		}
		return result, nil
	}, "Hello")

	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)

	require.Equal(t, "Mock!", result)
	env.AssertCalled(t, "namedActivity", mock.Anything, "Hello")
	env.AssertNotCalled(t, "namedActivity", mock.Anything, "Bye")
}

func TestActivityAssertNumberOfCalls(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	env.RegisterActivity(namedActivity)
	env.OnActivity(namedActivity, mock.Anything, mock.Anything).Return("Mock!", nil)

	env.ExecuteWorkflow(func(ctx Context, arg1 string) (string, error) {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		_ = ExecuteLocalActivity(ctx, "namedActivity", arg1).Get(ctx, &result)
		return result, nil
	}, "Hello")

	require.NoError(t, env.GetWorkflowError())
	env.AssertNumberOfCalls(t, "namedActivity", 3)
	env.AssertNumberOfCalls(t, "otherActivity", 0)
}

func HelloWorkflow(_ Context, name string) (string, error) {
	return "", errors.New("unimplemented")
}

func TestWorkflowMockingWithoutRegistration(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.OnWorkflow(HelloWorkflow, mock.Anything, mock.Anything).Return(
		func(ctx Context, person string) (string, error) {
			return "Hello " + person + "!", nil
		})
	env.ExecuteWorkflow("HelloWorkflow", "Temporal")
	require.NoError(t, env.GetWorkflowError())
	var result string
	err := env.GetWorkflowResult(&result)
	require.NoError(t, err)
	require.Equal(t, "Hello Temporal!", result)
}

func TestActivityMockingByNameWithoutRegistrationFails(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	assert.Panics(t, func() { env.OnActivity("SayHello", mock.Anything, mock.Anything) }, "The code did not panic")
}

func TestMockCallWrapperNotBefore(t *testing.T) {
	testSuite := &WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()
	env.RegisterActivity(namedActivity)

	c1 := env.OnActivity(namedActivity, mock.Anything, "call1").Return("result1", nil)
	env.OnActivity(namedActivity, mock.Anything, "call2").Return("result2", nil).NotBefore(c1)

	env.ExecuteWorkflow(func(ctx Context) error {
		ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{
			ScheduleToCloseTimeout: time.Hour,
			StartToCloseTimeout:    time.Hour,
		})
		var result string
		return ExecuteLocalActivity(ctx, "namedActivity", "call2").Get(ctx, &result)
	})
	var expectedErr *PanicError
	require.ErrorAs(t, env.GetWorkflowError(), &expectedErr)
	require.ErrorContains(t, expectedErr, "Must not be called before")
}

func TestCustomFailureConverter(t *testing.T) {
	t.Parallel()

	var suite WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetFailureConverter(testFailureConverter{
		fallback: defaultFailureConverter,
	})

	var calls atomic.Int32
	activity := func(context.Context) error {
		_ = calls.Add(1)
		return testCustomError{}
	}
	env.RegisterActivity(activity)

	env.ExecuteWorkflow(func(ctx Context) error {
		ctx = WithActivityOptions(ctx, ActivityOptions{
			StartToCloseTimeout: time.Hour,
		})
		return ExecuteActivity(ctx, activity).Get(ctx, nil)
	})
	require.True(t, env.IsWorkflowCompleted())

	// Failure converter should've reconstructed the custom error type.
	require.True(t, errors.As(env.GetWorkflowError(), &testCustomError{}))

	// Activity should've only been called once because the failure converter
	// set the NonRetryable flag.
	require.Equal(t, 1, int(calls.Load()))
}

type testCustomError struct{}

func (testCustomError) Error() string { return "this is a custom error type" }

type testFailureConverter struct {
	fallback converter.FailureConverter
}

func (c testFailureConverter) ErrorToFailure(err error) *failurepb.Failure {
	if errors.As(err, &testCustomError{}) {
		return &failurepb.Failure{
			FailureInfo: &failurepb.Failure_ApplicationFailureInfo{
				ApplicationFailureInfo: &failurepb.ApplicationFailureInfo{
					Type:         "CUSTOM ERROR",
					NonRetryable: true,
				},
			},
		}
	}
	return c.fallback.ErrorToFailure(err)
}

func (c testFailureConverter) FailureToError(failure *failurepb.Failure) error {
	if failure.GetApplicationFailureInfo().GetType() == "CUSTOM ERROR" {
		return testCustomError{}
	}
	return c.fallback.FailureToError(failure)
}
