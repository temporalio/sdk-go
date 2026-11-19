package internal

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservicemock/v1"

	ilog "go.temporal.io/sdk/internal/log"
)

type activityNameClashStruct struct{}

func (*activityNameClashStruct) ActivityNameClash(context.Context) (string, error) {
	return "method", nil
}

type activityNameClashValueStruct struct{}

func (activityNameClashValueStruct) ActivityNameClash(context.Context) (string, error) {
	return "value method", nil
}

type activityNameClashPromotedStruct struct{ *activityNameClashStruct }

func ActivityNameClash(context.Context) (string, error) { return "function", nil }

func TestActivityStructNameClash(t *testing.T) {
	for _, disableAliasing := range []bool{false, true} {
		for _, structFirst := range []bool{false, true} {
			for _, local := range []bool{false, true} {
				t.Run(fmt.Sprintf("disableAliasing=%v/structFirst=%v/local=%v", disableAliasing, structFirst, local), func(t *testing.T) {
					logger := ilog.NewMemoryLogger()
					var suite WorkflowTestSuite
					suite.SetLogger(logger)
					suite.SetDisableRegistrationAliasing(disableAliasing)
					env := suite.NewTestWorkflowEnvironment()
					activities := &activityNameClashStruct{}
					if structFirst {
						env.RegisterActivityWithOptions(activities, RegisterActivityOptions{Name: "Prefix_"})
						require.Empty(t, logger.Lines())
						env.RegisterActivity(ActivityNameClash)
					} else {
						env.RegisterActivity(ActivityNameClash)
						require.Empty(t, logger.Lines())
						env.RegisterActivityWithOptions(activities, RegisterActivityOptions{Name: "Prefix_"})
					}
					lines := logger.Lines()
					if disableAliasing {
						require.Empty(t, lines)
					} else {
						require.Len(t, lines, 1)
						require.Contains(t, lines[0], "WARN")
						require.Contains(t, lines[0], `Ambiguous activity name "ActivityNameClash"`)
						require.Contains(t, lines[0], "ActivityName ActivityNameClash")
						require.Contains(t, lines[0], "Prefix_ActivityNameClash")
						require.Contains(t, lines[0], "worker.Options.DisableRegistrationAliasing")
					}

					env.ExecuteWorkflow(func(ctx Context) ([]string, error) {
						ctx = WithActivityOptions(ctx, ActivityOptions{StartToCloseTimeout: time.Second})
						ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{StartToCloseTimeout: time.Second})
						var results []string
						for _, activity := range []any{ActivityNameClash, activities.ActivityNameClash, "Prefix_ActivityNameClash"} {
							var future Future
							if local {
								future = ExecuteLocalActivity(ctx, activity)
							} else {
								future = ExecuteActivity(ctx, activity)
							}
							var result string
							if err := future.Get(ctx, &result); err != nil {
								return nil, err
							}
							results = append(results, result)
						}
						return results, nil
					})
					var results []string
					require.NoError(t, env.GetWorkflowResult(&results))
					// Preserve the historical resolution of the method reference to the standalone function.
					require.Equal(t, []string{"function", "function", "method"}, results)
				})
			}
		}
	}
}

func TestActivityRegisteredNameClash(t *testing.T) {
	for _, disableAliasing := range []bool{false, true} {
		for _, aliasFirst := range []bool{false, true} {
			for _, local := range []bool{false, true} {
				t.Run(fmt.Sprintf("disableAliasing=%v/aliasFirst=%v/local=%v", disableAliasing, aliasFirst, local), func(t *testing.T) {
					logger := ilog.NewMemoryLogger()
					var suite WorkflowTestSuite
					suite.SetLogger(logger)
					suite.SetDisableRegistrationAliasing(disableAliasing)
					env := suite.NewTestWorkflowEnvironment()
					if aliasFirst {
						env.RegisterActivityWithOptions(aliasNameClash2, RegisterActivityOptions{Name: "Activity_v2"})
						require.Empty(t, logger.Lines())
						env.RegisterActivityWithOptions(aliasNameClash1, RegisterActivityOptions{Name: "aliasNameClash2"})
					} else {
						env.RegisterActivityWithOptions(aliasNameClash1, RegisterActivityOptions{Name: "aliasNameClash2"})
						require.Empty(t, logger.Lines())
						env.RegisterActivityWithOptions(aliasNameClash2, RegisterActivityOptions{Name: "Activity_v2"})
					}
					lines := logger.Lines()
					if disableAliasing {
						require.Empty(t, lines)
					} else {
						require.Len(t, lines, 1)
						require.Contains(t, lines[0], "WARN")
						require.Contains(t, lines[0], `Ambiguous activity name "aliasNameClash2"`)
						require.Contains(t, lines[0], "go.temporal.io/sdk/internal.aliasNameClash1")
						require.Contains(t, lines[0], "go.temporal.io/sdk/internal.aliasNameClash2")
						require.Contains(t, lines[0], `registered as "aliasNameClash2"`)
						require.Contains(t, lines[0], `registered as "Activity_v2"`)
						require.Contains(t, lines[0], "worker.Options.DisableRegistrationAliasing")
					}

					env.ExecuteWorkflow(func(ctx Context) (string, error) {
						ctx = WithActivityOptions(ctx, ActivityOptions{StartToCloseTimeout: time.Second})
						ctx = WithLocalActivityOptions(ctx, LocalActivityOptions{StartToCloseTimeout: time.Second})
						var future Future
						if local {
							future = ExecuteLocalActivity(ctx, "aliasNameClash2")
						} else {
							future = ExecuteActivity(ctx, "aliasNameClash2")
						}
						var result string
						err := future.Get(ctx, &result)
						return result, err
					})
					var result string
					require.NoError(t, env.GetWorkflowResult(&result))
					if disableAliasing {
						require.Equal(t, "func1", result)
					} else {
						// Preserve the historical alias lookup for registered string names.
						require.Equal(t, "func2", result)
					}
				})
			}
		}
	}
}

func TestActivityNameCollisionWarning(t *testing.T) {
	activities := &activityNameClashStruct{}
	tests := []struct {
		name         string
		first        any
		firstName    string
		second       any
		secondName   string
		wantWarnings int
	}{
		{
			name:  "different unqualified names",
			first: aliasNameClash1, firstName: "Function",
			second: activities, secondName: "Prefix_",
		},
		{
			name:  "same registration",
			first: activities, firstName: "Prefix_",
			second: activities, secondName: "Prefix_",
		},
		{
			name:  "prefixed structs",
			first: activities, firstName: "First_",
			second: activities, secondName: "Second_",
			wantWarnings: 1,
		},
		{
			name:  "individually registered method",
			first: activities.ActivityNameClash, firstName: "Method",
			second: activities, secondName: "Prefix_",
			wantWarnings: 1,
		},
		{
			name:  "function aliases",
			first: aliasNameClash1, firstName: "First",
			second: aliasNameClash1, secondName: "Second",
			wantWarnings: 1,
		},
		{
			name:  "registered name collides with method name",
			first: aliasNameClash1, firstName: "ActivityNameClash",
			second: activities, secondName: "Prefix_",
			wantWarnings: 1,
		},
		{
			name:  "method name collides with registered name",
			first: activities, firstName: "Prefix_",
			second: aliasNameClash1, secondName: "ActivityNameClash",
			wantWarnings: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, disableAliasing := range []bool{false, true} {
				t.Run(fmt.Sprintf("disableAliasing=%v", disableAliasing), func(t *testing.T) {
					logger := ilog.NewMemoryLogger()
					registry := newRegistryWithOptions(registryOptions{
						disableAliasing: disableAliasing,
						logger:          logger,
					})
					registry.RegisterActivityWithOptions(tt.first, RegisterActivityOptions{Name: tt.firstName})
					require.Empty(t, logger.Lines())
					registry.RegisterActivityWithOptions(tt.second, RegisterActivityOptions{
						Name:                          tt.secondName,
						DisableAlreadyRegisteredCheck: true,
					})
					if disableAliasing {
						require.Empty(t, logger.Lines())
					} else {
						require.Len(t, logger.Lines(), tt.wantWarnings)
					}
				})
			}
		})
	}
}

func TestActivityNameCollisionLocations(t *testing.T) {
	activities := &activityNameClashStruct{}
	method, ok := reflect.TypeOf(activities).MethodByName("ActivityNameClash")
	require.True(t, ok)
	function := runtime.FuncForPC(reflect.ValueOf(ActivityNameClash).Pointer())
	tests := []struct {
		name           string
		activity       any
		nameOption     string
		registeredName string
		goName         string
		definition     *runtime.Func
	}{
		{
			name:           "named function",
			activity:       ActivityNameClash,
			nameOption:     "Alias",
			registeredName: "Alias",
			goName:         "go.temporal.io/sdk/internal.ActivityNameClash",
			definition:     function,
		},
		{
			name:           "struct method",
			activity:       activities,
			nameOption:     "Prefix_",
			registeredName: "Prefix_ActivityNameClash",
			goName:         "go.temporal.io/sdk/internal.(*activityNameClashStruct).ActivityNameClash",
			definition:     runtime.FuncForPC(method.Func.Pointer()),
		},
		{
			name:           "bound method",
			activity:       activities.ActivityNameClash,
			nameOption:     "Method",
			registeredName: "Method",
			goName:         "go.temporal.io/sdk/internal.(*activityNameClashStruct).ActivityNameClash",
		},
		{
			name:           "value receiver through pointer",
			activity:       &activityNameClashValueStruct{},
			nameOption:     "Value_",
			registeredName: "Value_ActivityNameClash",
			goName:         "go.temporal.io/sdk/internal.(*activityNameClashValueStruct).ActivityNameClash",
		},
		{
			name:           "promoted method",
			activity:       &activityNameClashPromotedStruct{activities},
			nameOption:     "Promoted_",
			registeredName: "Promoted_ActivityNameClash",
			goName:         "go.temporal.io/sdk/internal.(*activityNameClashPromotedStruct).ActivityNameClash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			var suite WorkflowTestSuite
			suite.SetLogger(logger)
			env := suite.NewTestActivityEnvironment()
			_, registrationFile, firstLine, _ := runtime.Caller(0)
			env.RegisterActivity(ActivityNameClash)
			_, _, secondLine, _ := runtime.Caller(0)
			env.RegisterActivityWithOptions(tt.activity, RegisterActivityOptions{Name: tt.nameOption})

			lines := logger.Lines()
			require.Len(t, lines, 1)
			require.Contains(t, lines[0], `Ambiguous activity name "ActivityNameClash"`)
			require.Contains(t, lines[0], fmt.Sprintf("registered as %q at %s:%d", "ActivityNameClash", registrationFile, firstLine+1))
			require.Contains(t, lines[0], fmt.Sprintf("registered as %q at %s:%d", tt.registeredName, registrationFile, secondLine+1))
			file, line := function.FileLine(function.Entry())
			require.Contains(t, lines[0], fmt.Sprintf("%s (%s:%d)", function.Name(), file, line))
			require.Contains(t, lines[0], tt.goName)
			if tt.definition != nil {
				file, line := tt.definition.FileLine(tt.definition.Entry())
				require.Contains(t, lines[0], fmt.Sprintf("%s (%s:%d)", tt.goName, file, line))
			}
			require.NotContains(t, lines[0], "<autogenerated>")
			require.NotContains(t, lines[0], "reflect.methodValueCall")
			require.NotContains(t, lines[0], "-fm")
		})
	}
}

func TestReflectedActivityNameCollisionLocations(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	var suite WorkflowTestSuite
	suite.SetLogger(logger)
	env := suite.NewTestActivityEnvironment()
	first := reflect.ValueOf(&activityNameClashStruct{}).MethodByName("ActivityNameClash").Interface()
	second := reflect.ValueOf(&activityNameClashValueStruct{}).MethodByName("ActivityNameClash").Interface()
	_, registrationFile, firstLine, _ := runtime.Caller(0)
	env.RegisterActivityWithOptions(first, RegisterActivityOptions{Name: "First"})
	_, _, secondLine, _ := runtime.Caller(0)
	env.RegisterActivityWithOptions(second, RegisterActivityOptions{Name: "Second"})

	lines := logger.Lines()
	require.Len(t, lines, 1)
	require.Contains(t, lines[0], `Ambiguous activity name "methodValueCall"`)
	require.Contains(t, lines[0], fmt.Sprintf("reflected method, registered as %q at %s:%d", "First", registrationFile, firstLine+1))
	require.Contains(t, lines[0], fmt.Sprintf("reflected method, registered as %q at %s:%d", "Second", registrationFile, secondLine+1))
	require.NotContains(t, lines[0], "<autogenerated>")
	require.NotContains(t, lines[0], "reflect.methodValueCall")
}

func TestWorkerActivityNameCollisionWarning(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
	client := NewServiceClient(service, nil, ClientOptions{Logger: logger})
	worker := NewAggregatedWorker(client, "test-task-queue", WorkerOptions{})
	_, registrationFile, firstLine, _ := runtime.Caller(0)
	worker.RegisterActivity(ActivityNameClash)
	_, _, secondLine, _ := runtime.Caller(0)
	worker.RegisterActivityWithOptions(&activityNameClashStruct{}, RegisterActivityOptions{Name: "Prefix_"})

	var warnings []string
	for _, line := range logger.Lines() {
		if strings.Contains(line, "Ambiguous activity name") {
			warnings = append(warnings, line)
		}
	}
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "WARN")
	require.Contains(t, warnings[0], "ActivityName ActivityNameClash")
	require.Contains(t, warnings[0], "RegisteredActivityName Prefix_ActivityNameClash")
	require.Contains(t, warnings[0], "ExistingRegisteredActivityName ActivityNameClash")
	require.Contains(t, warnings[0], fmt.Sprintf("registered as %q at %s:%d", "ActivityNameClash", registrationFile, firstLine+1))
	require.Contains(t, warnings[0], fmt.Sprintf("registered as %q at %s:%d", "Prefix_ActivityNameClash", registrationFile, secondLine+1))
}

func workflowNameClash1(Context) (string, error) { return "workflow1", nil }
func workflowNameClash2(Context) (string, error) { return "workflow2", nil }

type workflowNameClashStruct struct{}

func (*workflowNameClashStruct) workflowNameClash1(Context) (string, error) {
	return "method", nil
}

func TestWorkflowNameCollisionWarning(t *testing.T) {
	workflows := &workflowNameClashStruct{}
	tests := []struct {
		name         string
		first        any
		firstName    string
		second       any
		secondName   string
		wantWarnings int
	}{
		{
			name:  "different unqualified names",
			first: workflowNameClash1, firstName: "First",
			second: workflowNameClash2, secondName: "Second",
		},
		{
			name:  "same registration",
			first: workflowNameClash1, firstName: "First",
			second: workflowNameClash1, secondName: "First",
		},
		{
			name:  "function aliases",
			first: workflowNameClash1, firstName: "First",
			second: workflowNameClash1, secondName: "Second",
			wantWarnings: 1,
		},
		{
			name:  "registered name collides with function name",
			first: workflowNameClash1, firstName: "workflowNameClash2",
			second: workflowNameClash2, secondName: "Workflow_v2",
			wantWarnings: 1,
		},
		{
			name:  "function name collides with registered name",
			first: workflowNameClash2, firstName: "Workflow_v2",
			second: workflowNameClash1, secondName: "workflowNameClash2",
			wantWarnings: 1,
		},
		{
			name:  "method name collides with function name",
			first: workflowNameClash1, firstName: "First",
			second: workflows.workflowNameClash1, secondName: "Method",
			wantWarnings: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, disableAliasing := range []bool{false, true} {
				t.Run(fmt.Sprintf("disableAliasing=%v", disableAliasing), func(t *testing.T) {
					logger := ilog.NewMemoryLogger()
					var suite WorkflowTestSuite
					suite.SetLogger(logger)
					suite.SetDisableRegistrationAliasing(disableAliasing)
					env := suite.NewTestWorkflowEnvironment()
					env.RegisterWorkflowWithOptions(tt.first, RegisterWorkflowOptions{Name: tt.firstName})
					require.Empty(t, logger.Lines())
					env.RegisterWorkflowWithOptions(tt.second, RegisterWorkflowOptions{
						Name:                          tt.secondName,
						DisableAlreadyRegisteredCheck: true,
					})
					if disableAliasing {
						require.Empty(t, logger.Lines())
					} else {
						require.Len(t, logger.Lines(), tt.wantWarnings)
					}
				})
			}
		})
	}
}

func TestWorkerWorkflowNameCollisionWarning(t *testing.T) {
	for _, disableAliasing := range []bool{false, true} {
		t.Run(fmt.Sprintf("disableAliasing=%v", disableAliasing), func(t *testing.T) {
			logger := ilog.NewMemoryLogger()
			service := workflowservicemock.NewMockWorkflowServiceClient(gomock.NewController(t))
			client := NewServiceClient(service, nil, ClientOptions{Logger: logger})
			worker := NewAggregatedWorker(client, "test-task-queue", WorkerOptions{DisableRegistrationAliasing: disableAliasing})
			_, registrationFile, firstLine, _ := runtime.Caller(0)
			worker.RegisterWorkflow(workflowNameClash1)
			_, _, secondLine, _ := runtime.Caller(0)
			worker.RegisterWorkflowWithOptions((&workflowNameClashStruct{}).workflowNameClash1, RegisterWorkflowOptions{Name: "Method"})

			var warnings []string
			for _, line := range logger.Lines() {
				if strings.Contains(line, "Ambiguous workflow name") {
					warnings = append(warnings, line)
				}
			}
			if disableAliasing {
				require.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, 1)
			require.Contains(t, warnings[0], "WARN")
			require.Contains(t, warnings[0], `Ambiguous workflow name "workflowNameClash1"`)
			require.Contains(t, warnings[0], "WorkflowName workflowNameClash1")
			require.Contains(t, warnings[0], "RegisteredWorkflowName Method")
			require.Contains(t, warnings[0], "ExistingRegisteredWorkflowName workflowNameClash1")
			require.Contains(t, warnings[0], fmt.Sprintf("registered as %q at %s:%d", "workflowNameClash1", registrationFile, firstLine+1))
			require.Contains(t, warnings[0], fmt.Sprintf("registered as %q at %s:%d", "Method", registrationFile, secondLine+1))
			function := runtime.FuncForPC(reflect.ValueOf(workflowNameClash1).Pointer())
			file, line := function.FileLine(function.Entry())
			require.Contains(t, warnings[0], fmt.Sprintf("%s (%s:%d)", function.Name(), file, line))
			require.Contains(t, warnings[0], "go.temporal.io/sdk/internal.(*workflowNameClashStruct).workflowNameClash1")
			require.Contains(t, warnings[0], "worker.Options.DisableRegistrationAliasing")
			require.NotContains(t, warnings[0], "<autogenerated>")
			require.NotContains(t, warnings[0], "-fm")
		})
	}
}

func TestWorkflowAndActivityNameCollisionNamespaces(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	registry := newRegistryWithOptions(registryOptions{logger: logger})
	registry.RegisterWorkflowWithOptions(workflowNameClash1, RegisterWorkflowOptions{Name: "ActivityNameClash"})
	registry.RegisterActivityWithOptions(ActivityNameClash, RegisterActivityOptions{Name: "workflowNameClash1"})
	require.Empty(t, logger.Lines())
}

func TestNameCollisionRegistrySnapshot(t *testing.T) {
	logger := ilog.NewMemoryLogger()
	var suite WorkflowTestSuite
	suite.SetLogger(logger)
	env := suite.NewTestWorkflowEnvironment()
	restore := env.impl.snapshotRegistry()
	env.RegisterWorkflowWithOptions(workflowNameClash1, RegisterWorkflowOptions{Name: "TemporaryWorkflow"})
	env.RegisterActivityWithOptions(ActivityNameClash, RegisterActivityOptions{Name: "TemporaryActivity"})
	require.Empty(t, logger.Lines())

	restore()
	env.RegisterWorkflowWithOptions(workflowNameClash1, RegisterWorkflowOptions{Name: "Workflow"})
	env.RegisterActivityWithOptions(ActivityNameClash, RegisterActivityOptions{Name: "Activity"})
	require.Empty(t, logger.Lines())
}
