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

// All code in this file is private to the package.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"github.com/golang/mock/gomock"
	"github.com/opentracing/opentracing-go"
	"github.com/pborman/uuid"
	"github.com/uber-go/tally"

	commonpb "go.temporal.io/temporal-proto/common"
	decisionpb "go.temporal.io/temporal-proto/decision"
	eventpb "go.temporal.io/temporal-proto/event"
	filterpb "go.temporal.io/temporal-proto/filter"
	"go.temporal.io/temporal-proto/serviceerror"
	"go.temporal.io/temporal-proto/workflowservice"
	"go.temporal.io/temporal-proto/workflowservicemock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"go.temporal.io/temporal/internal/common/backoff"
	"go.temporal.io/temporal/internal/common/metrics"
	"go.temporal.io/temporal/internal/common/serializer"
)

const (
	// Set to 2 pollers for now, can adjust later if needed. The typical RTT (round-trip time) is below 1ms within data
	// center. And the poll API latency is about 5ms. With 2 poller, we could achieve around 300~400 RPS.
	defaultConcurrentPollRoutineSize = 2

	defaultMaxConcurrentActivityExecutionSize = 1000   // Large concurrent activity execution size (1k)
	defaultWorkerActivitiesPerSecond          = 100000 // Large activity executions/sec (unlimited)

	defaultMaxConcurrentLocalActivityExecutionSize = 1000   // Large concurrent activity execution size (1k)
	defaultWorkerLocalActivitiesPerSecond          = 100000 // Large activity executions/sec (unlimited)

	defaultTaskListActivitiesPerSecond = 100000.0 // Large activity executions/sec (unlimited)

	defaultMaxConcurrentTaskExecutionSize = 1000   // hardcoded max task execution size.
	defaultWorkerTaskExecutionRate        = 100000 // Large task execution rate (unlimited)

	defaultPollerRate = 1000

	defaultMaxConcurrentSessionExecutionSize = 1000 // Large concurrent session execution size (1k)

	testTagsContextKey = "temporal-testTags"
)

type (
	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	workflowWorker struct {
		executionParameters workerExecutionParameters
		workflowService     workflowservice.WorkflowServiceClient
		poller              taskPoller // taskPoller to poll and process the tasks.
		worker              *baseWorker
		localActivityWorker *baseWorker
		identity            string
		stopC               chan struct{}
	}

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	activityWorker struct {
		executionParameters workerExecutionParameters
		workflowService     workflowservice.WorkflowServiceClient
		poller              taskPoller
		worker              *baseWorker
		identity            string
		stopC               chan struct{}
	}

	// sessionWorker wraps the code for hosting session creation, completion and
	// activities within a session. The creationWorker polls from a global tasklist,
	// while the activityWorker polls from a resource specific tasklist.
	sessionWorker struct {
		creationWorker *activityWorker
		activityWorker *activityWorker
	}

	// Worker overrides.
	workerOverrides struct {
		workflowTaskHandler WorkflowTaskHandler
		activityTaskHandler ActivityTaskHandler
	}

	// workerExecutionParameters defines worker configure/execution options.
	workerExecutionParameters struct {
		// Namespace name.
		Namespace string

		// Task list name to poll.
		TaskList string

		// Defines how many concurrent activity executions by this worker.
		ConcurrentActivityExecutionSize int

		// Defines rate limiting on number of activity tasks that can be executed per second per worker.
		WorkerActivitiesPerSecond float64

		// MaxConcurrentActivityPollers is the max number of pollers for activity task list
		MaxConcurrentActivityPollers int

		// Defines how many concurrent decision task executions by this worker.
		ConcurrentDecisionTaskExecutionSize int

		// Defines rate limiting on number of decision tasks that can be executed per second per worker.
		WorkerDecisionTasksPerSecond float64

		// MaxConcurrentDecisionPollers is the max number of pollers for decision task list
		MaxConcurrentDecisionPollers int

		// Defines how many concurrent local activity executions by this worker.
		ConcurrentLocalActivityExecutionSize int

		// Defines rate limiting on number of local activities that can be executed per second per worker.
		WorkerLocalActivitiesPerSecond float64

		// TaskListActivitiesPerSecond is the throttling limit for activity tasks controlled by the server
		TaskListActivitiesPerSecond float64

		// User can provide an identity for the debuggability. If not provided the framework has
		// a default option.
		Identity string

		MetricsScope tally.Scope

		Logger *zap.Logger

		// Enable logging in replay mode
		EnableLoggingInReplay bool

		// Context to store user provided key/value pairs
		UserContext context.Context

		// Context cancel function to cancel user context
		UserContextCancel context.CancelFunc

		// Disable sticky execution
		DisableStickyExecution bool

		StickyScheduleToStartTimeout time.Duration

		// WorkflowPanicPolicy is used for configuring how client's decision task handler deals with workflow
		// code panicking which includes non backwards compatible changes to the workflow code without appropriate
		// versioning (see workflow.GetVersion).
		// The default behavior is to block workflow execution until the problem is fixed.
		WorkflowPanicPolicy WorkflowPanicPolicy

		DataConverter DataConverter

		// WorkerStopTimeout is the time delay before hard terminate worker
		WorkerStopTimeout time.Duration

		// WorkerStopChannel is a read only channel listen on worker close. The worker will close the channel before exit.
		WorkerStopChannel <-chan struct{}

		// SessionResourceID is a unique identifier of the resource the session will consume
		SessionResourceID string

		ContextPropagators []ContextPropagator

		Tracer opentracing.Tracer
	}
)

// newWorkflowWorker returns an instance of the workflow worker.
func newWorkflowWorker(service workflowservice.WorkflowServiceClient, params workerExecutionParameters, ppMgr pressurePointMgr, registry *registry) *workflowWorker {
	return newWorkflowWorkerInternal(service, params, ppMgr, nil, registry)
}

func ensureRequiredParams(params *workerExecutionParameters) {
	if params.Identity == "" {
		params.Identity = getWorkerIdentity(params.TaskList)
	}
	if params.Logger == nil {
		// create default logger if user does not supply one.
		config := zap.NewProductionConfig()
		// set default time formatter to "2006-01-02T15:04:05.000Z0700"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		// config.Level.SetLevel(zapcore.DebugLevel)
		logger, _ := config.Build()
		params.Logger = logger
		params.Logger.Info("No logger configured for temporal worker. Created default one.")
	}
	if params.MetricsScope == nil {
		params.MetricsScope = tally.NoopScope
		params.Logger.Info("No metrics scope configured for temporal worker. Use NoopScope as default.")
	}
	if params.DataConverter == nil {
		params.DataConverter = getDefaultDataConverter()
		params.Logger.Info("No DataConverter configured for temporal worker. Use default one.")
	}
}

// verifyNamespaceExist does a DescribeNamespace operation on the specified namespace with backoff/retry
// It returns an error, if the server returns an EntityNotExist or BadRequest error
// On any other transient error, this method will just return success
func verifyNamespaceExist(client workflowservice.WorkflowServiceClient, namespace string, logger *zap.Logger) error {
	ctx := context.Background()
	descNamespaceOp := func() error {
		tchCtx, cancel := newChannelContext(ctx)
		defer cancel()
		_, err := client.DescribeNamespace(tchCtx, &workflowservice.DescribeNamespaceRequest{Name: namespace})
		if err != nil {
			switch err.(type) {
			case *serviceerror.NotFound:
				logger.Error("namespace does not exist", zap.String("namespace", namespace), zap.Error(err))
				return err
			case *serviceerror.InvalidArgument:
				logger.Error("namespace does not exist", zap.String("namespace", namespace), zap.Error(err))
				return err
			}
			// on any other error, just return true
			logger.Warn("unable to verify if namespace exist", zap.String("namespace", namespace), zap.Error(err))
		}
		return nil
	}

	if namespace == "" {
		return errors.New("namespace cannot be empty")
	}

	// exponential backoff retry for upto a minute
	return backoff.Retry(ctx, descNamespaceOp, createDynamicServiceRetryPolicy(ctx), isServiceTransientError)
}

func newWorkflowWorkerInternal(service workflowservice.WorkflowServiceClient, params workerExecutionParameters, ppMgr pressurePointMgr, overrides *workerOverrides, registry *registry) *workflowWorker {
	workerStopChannel := make(chan struct{})
	params.WorkerStopChannel = getReadOnlyChannel(workerStopChannel)
	// Get a workflow task handler.
	ensureRequiredParams(&params)
	var taskHandler WorkflowTaskHandler
	if overrides != nil && overrides.workflowTaskHandler != nil {
		taskHandler = overrides.workflowTaskHandler
	} else {
		taskHandler = newWorkflowTaskHandler(params, ppMgr, registry)
	}
	return newWorkflowTaskWorkerInternal(taskHandler, service, params, workerStopChannel)
}

func newWorkflowTaskWorkerInternal(taskHandler WorkflowTaskHandler, service workflowservice.WorkflowServiceClient, params workerExecutionParameters, stopC chan struct{}) *workflowWorker {
	ensureRequiredParams(&params)
	poller := newWorkflowTaskPoller(taskHandler, service, params)
	worker := newBaseWorker(baseWorkerOptions{
		pollerCount:       params.MaxConcurrentDecisionPollers,
		pollerRate:        defaultPollerRate,
		maxConcurrentTask: params.ConcurrentDecisionTaskExecutionSize,
		maxTaskPerSecond:  params.WorkerDecisionTasksPerSecond,
		taskWorker:        poller,
		identity:          params.Identity,
		workerType:        "DecisionWorker",
		shutdownTimeout:   params.WorkerStopTimeout},
		params.Logger,
		params.MetricsScope,
		nil,
	)

	// laTunnel is the glue that hookup 3 parts
	laTunnel := newLocalActivityTunnel(params.WorkerStopChannel)

	// 1) workflow handler will send local activity task to laTunnel
	if handlerImpl, ok := taskHandler.(*workflowTaskHandlerImpl); ok {
		handlerImpl.laTunnel = laTunnel
	}

	// 2) local activity task poller will poll from laTunnel, and result will be pushed to laTunnel
	localActivityTaskPoller := newLocalActivityPoller(params, laTunnel)
	localActivityWorker := newBaseWorker(baseWorkerOptions{
		pollerCount:       1, // 1 poller (from local channel) is enough for local activity
		maxConcurrentTask: params.ConcurrentLocalActivityExecutionSize,
		maxTaskPerSecond:  params.WorkerLocalActivitiesPerSecond,
		taskWorker:        localActivityTaskPoller,
		identity:          params.Identity,
		workerType:        "LocalActivityWorker",
		shutdownTimeout:   params.WorkerStopTimeout},
		params.Logger,
		params.MetricsScope,
		nil,
	)

	// 3) the result pushed to laTunnel will be send as task to workflow worker to process.
	worker.taskQueueCh = laTunnel.resultCh

	return &workflowWorker{
		executionParameters: params,
		workflowService:     service,
		poller:              poller,
		worker:              worker,
		localActivityWorker: localActivityWorker,
		identity:            params.Identity,
		stopC:               stopC,
	}
}

// Start the worker.
func (ww *workflowWorker) Start() error {
	err := verifyNamespaceExist(ww.workflowService, ww.executionParameters.Namespace, ww.worker.logger)
	if err != nil {
		return err
	}
	ww.localActivityWorker.Start()
	ww.worker.Start()
	return nil // TODO: propagate error
}

func (ww *workflowWorker) Run() error {
	err := verifyNamespaceExist(ww.workflowService, ww.executionParameters.Namespace, ww.worker.logger)
	if err != nil {
		return err
	}
	ww.localActivityWorker.Start()
	ww.worker.Run()
	return nil
}

// Shutdown the worker.
func (ww *workflowWorker) Stop() {
	close(ww.stopC)
	// TODO: remove the stop methods in favor of the workerStopChannel
	ww.localActivityWorker.Stop()
	ww.worker.Stop()
}

func newSessionWorker(service workflowservice.WorkflowServiceClient, params workerExecutionParameters, overrides *workerOverrides, env *registry, maxConcurrentSessionExecutionSize int) *sessionWorker {
	if params.Identity == "" {
		params.Identity = getWorkerIdentity(params.TaskList)
	}
	// For now resourceID is hidden from user so we will always create a unique one for each worker.
	if params.SessionResourceID == "" {
		params.SessionResourceID = uuid.New()
	}
	sessionEnvironment := newSessionEnvironment(params.SessionResourceID, maxConcurrentSessionExecutionSize)

	creationTasklist := getCreationTasklist(params.TaskList)
	params.UserContext = context.WithValue(params.UserContext, sessionEnvironmentContextKey, sessionEnvironment)
	params.TaskList = sessionEnvironment.GetResourceSpecificTasklist()
	activityWorker := newActivityWorker(service, params, overrides, env, nil)

	params.MaxConcurrentActivityPollers = 1
	params.TaskList = creationTasklist
	creationWorker := newActivityWorker(service, params, overrides, env, sessionEnvironment.GetTokenBucket())

	return &sessionWorker{
		creationWorker: creationWorker,
		activityWorker: activityWorker,
	}
}

func (sw *sessionWorker) Start() error {
	err := sw.creationWorker.Start()
	if err != nil {
		return err
	}

	err = sw.activityWorker.Start()
	if err != nil {
		sw.creationWorker.Stop()
		return err
	}
	return nil
}

func (sw *sessionWorker) Run() error {
	err := sw.creationWorker.Start()
	if err != nil {
		return err
	}
	return sw.activityWorker.Run()
}

func (sw *sessionWorker) Stop() {
	sw.creationWorker.Stop()
	sw.activityWorker.Stop()
}

func newActivityWorker(service workflowservice.WorkflowServiceClient, params workerExecutionParameters, overrides *workerOverrides, env *registry, sessionTokenBucket *sessionTokenBucket) *activityWorker {
	workerStopChannel := make(chan struct{}, 1)
	params.WorkerStopChannel = getReadOnlyChannel(workerStopChannel)
	ensureRequiredParams(&params)

	// Get a activity task handler.
	var taskHandler ActivityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(service, params, env)
	}
	return newActivityTaskWorker(taskHandler, service, params, sessionTokenBucket, workerStopChannel)
}

func newActivityTaskWorker(taskHandler ActivityTaskHandler, service workflowservice.WorkflowServiceClient, workerParams workerExecutionParameters, sessionTokenBucket *sessionTokenBucket, stopC chan struct{}) (worker *activityWorker) {
	ensureRequiredParams(&workerParams)

	poller := newActivityTaskPoller(taskHandler, service, workerParams)

	base := newBaseWorker(
		baseWorkerOptions{
			pollerCount:       workerParams.MaxConcurrentActivityPollers,
			pollerRate:        defaultPollerRate,
			maxConcurrentTask: workerParams.ConcurrentActivityExecutionSize,
			maxTaskPerSecond:  workerParams.WorkerActivitiesPerSecond,
			taskWorker:        poller,
			identity:          workerParams.Identity,
			workerType:        "ActivityWorker",
			shutdownTimeout:   workerParams.WorkerStopTimeout,
			userContextCancel: workerParams.UserContextCancel},
		workerParams.Logger,
		workerParams.MetricsScope,
		sessionTokenBucket,
	)

	return &activityWorker{
		executionParameters: workerParams,
		workflowService:     service,
		worker:              base,
		poller:              poller,
		identity:            workerParams.Identity,
		stopC:               stopC,
	}
}

// Start the worker.
func (aw *activityWorker) Start() error {
	err := verifyNamespaceExist(aw.workflowService, aw.executionParameters.Namespace, aw.worker.logger)
	if err != nil {
		return err
	}
	aw.worker.Start()
	return nil // TODO: propagate errors
}

// Run the worker.
func (aw *activityWorker) Run() error {
	err := verifyNamespaceExist(aw.workflowService, aw.executionParameters.Namespace, aw.worker.logger)
	if err != nil {
		return err
	}
	aw.worker.Run()
	return nil
}

// Shutdown the worker.
func (aw *activityWorker) Stop() {
	close(aw.stopC)
	aw.worker.Stop()
}

type registry struct {
	sync.Mutex
	workflowFuncMap      map[string]interface{}
	workflowAliasMap     map[string]string
	activityFuncMap      map[string]activity
	activityAliasMap     map[string]string
	workflowInterceptors []WorkflowInterceptorFactory
}

func (r *registry) WorkflowInterceptors() []WorkflowInterceptorFactory {
	return r.workflowInterceptors
}

func (r *registry) SetWorkflowInterceptors(workflowInterceptors []WorkflowInterceptorFactory) {
	r.workflowInterceptors = workflowInterceptors
}

func (r *registry) RegisterWorkflow(af interface{}) {
	r.RegisterWorkflowWithOptions(af, RegisterWorkflowOptions{})
}

func (r *registry) RegisterWorkflowWithOptions(
	af interface{},
	options RegisterWorkflowOptions,
) {
	// Support direct registration of workflowDefinition
	wd, ok := af.(workflowDefinition)
	if ok {
		r.addWorkflowFn(options.Name, wd)
		return
	}
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := validateFnFormat(fnType, true); err != nil {
		panic(err)
	}
	fnName := getFunctionName(af)
	alias := options.Name
	registerName := fnName
	if len(alias) > 0 {
		registerName = alias
	}
	if !options.DisableAlreadyRegisteredCheck {
		if _, ok := r.workflowFuncMap[registerName]; ok {
			panic(fmt.Sprintf("workflow name \"%v\" is already registered", registerName))
		}
	}
	r.addWorkflowFn(registerName, af)
	if len(alias) > 0 {
		r.addWorkflowAlias(fnName, alias)
	}
}

func (r *registry) RegisterActivity(af interface{}) {
	r.RegisterActivityWithOptions(af, RegisterActivityOptions{})
}

func (r *registry) RegisterActivityWithOptions(
	af interface{},
	options RegisterActivityOptions,
) {
	// Support direct registration of activity
	a, ok := af.(activity)
	if ok {
		r.addActivity(options.Name, a)
		return
	}
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if fnType.Kind() == reflect.Ptr && fnType.Elem().Kind() == reflect.Struct {
		_ = r.registerActivityStructWithOptions(af, options)
		return
	}
	if err := validateFnFormat(fnType, false); err != nil {
		panic(err)
	}
	fnName := getFunctionName(af)
	alias := options.Name
	registerName := fnName
	if len(alias) > 0 {
		registerName = alias
	}
	if !options.DisableAlreadyRegisteredCheck {
		if _, ok := r.activityFuncMap[registerName]; ok {
			panic(fmt.Sprintf("activity type \"%v\" is already registered", registerName))
		}
	}
	r.addActivityFn(registerName, af)
	if len(alias) > 0 {
		r.addActivityAlias(fnName, alias)
	}
}

func (r *registry) registerActivityStructWithOptions(aStruct interface{}, options RegisterActivityOptions) error {
	structValue := reflect.ValueOf(aStruct)
	structType := structValue.Type()
	count := 0
	for i := 0; i < structValue.NumMethod(); i++ {
		methodValue := structValue.Method(i)
		method := structType.Method(i)
		// skip private method
		if method.PkgPath != "" {
			continue
		}
		name := method.Name
		if err := validateFnFormat(method.Type, false); err != nil {
			return fmt.Errorf("method %v of %v: %e", name, structType.Name(), err)
		}
		registerName := options.Name + name
		if !options.DisableAlreadyRegisteredCheck {
			if _, ok := r.getActivityFn(registerName); ok {
				return fmt.Errorf("activity type \"%v\" is already registered", registerName)
			}
		}
		r.addActivityFn(registerName, methodValue.Interface())
		count++
	}
	if count == 0 {
		return fmt.Errorf("no activities (public methods) found at %v structure", structType.Name())
	}
	return nil
}

func (r *registry) addWorkflowAlias(fnName string, alias string) {
	r.Lock()
	defer r.Unlock()
	r.workflowAliasMap[fnName] = alias
}

func (r *registry) getWorkflowAlias(fnName string) (string, bool) {
	r.Lock()
	defer r.Unlock()
	alias, ok := r.workflowAliasMap[fnName]
	return alias, ok
}

func (r *registry) addWorkflowFn(fnName string, wf interface{}) {
	r.Lock()
	defer r.Unlock()
	if r.workflowFuncMap == nil {
		panic("nil workflowFuncMap: registry must be created with newRegistry")
	}
	r.workflowFuncMap[fnName] = wf
}

func (r *registry) getWorkflowFn(fnName string) (interface{}, bool) {
	r.Lock()
	defer r.Unlock()
	fn, ok := r.workflowFuncMap[fnName]
	return fn, ok
}

func (r *registry) getRegisteredWorkflowTypes() []string {
	r.Lock()
	defer r.Unlock()
	var result []string
	for t := range r.workflowFuncMap {
		result = append(result, t)
	}
	return result
}

func (r *registry) addActivityAlias(fnName string, alias string) {
	r.Lock()
	defer r.Unlock()
	r.activityAliasMap[fnName] = alias
}

func (r *registry) getActivityAlias(fnName string) (string, bool) {
	r.Lock()
	defer r.Unlock()
	alias, ok := r.activityAliasMap[fnName]
	return alias, ok
}

func (r *registry) addActivity(fnName string, a activity) {
	r.Lock()
	defer r.Unlock()
	r.activityFuncMap[fnName] = a
}

func (r *registry) addActivityFn(fnName string, af interface{}) {
	r.addActivity(fnName, &activityExecutor{fnName, af})
}

func (r *registry) getActivity(fnName string) (activity, bool) {
	r.Lock()
	defer r.Unlock()
	a, ok := r.activityFuncMap[fnName]
	return a, ok
}

func (r *registry) getActivityFn(fnName string) (interface{}, bool) {
	if a, ok := r.getActivity(fnName); ok {
		return a.GetFunction(), ok
	}
	return nil, false
}

func (r *registry) getRegisteredActivities() []activity {
	r.Lock()
	defer r.Unlock()
	activities := make([]activity, 0, len(r.activityFuncMap))
	for _, a := range r.activityFuncMap {
		activities = append(activities, a)
	}
	return activities
}

func (r *registry) getRegisteredActivityTypes() []string {
	r.Lock()
	defer r.Unlock()
	var result []string
	for name := range r.activityFuncMap {
		result = append(result, name)
	}
	return result
}

func (r *registry) getWorkflowDefinition(wt WorkflowType) (workflowDefinition, error) {
	lookup := wt.Name
	if alias, ok := r.getWorkflowAlias(lookup); ok {
		lookup = alias
	}
	wf, ok := r.getWorkflowFn(lookup)
	if !ok {
		supported := strings.Join(r.getRegisteredWorkflowTypes(), ", ")
		return nil, fmt.Errorf("unable to find workflow type: %v. Supported types: [%v]", lookup, supported)
	}
	wd, ok := wf.(workflowDefinition)
	if ok {
		return wd, nil
	}
	executor := &workflowExecutor{workflowType: lookup, fn: wf, interceptors: r.getInterceptors()}
	return newSyncWorkflowDefinition(executor), nil
}

func (r *registry) getInterceptors() []WorkflowInterceptorFactory {
	return r.workflowInterceptors
}

// Validate function parameters.
func validateFnFormat(fnType reflect.Type, isWorkflow bool) error {
	if fnType.Kind() != reflect.Func {
		return fmt.Errorf("expected a func as input but was %s", fnType.Kind())
	}
	if isWorkflow {
		if fnType.NumIn() < 1 {
			return fmt.Errorf(
				"expected at least one argument of type workflow.Context in function, found %d input arguments",
				fnType.NumIn(),
			)
		}
		if !isWorkflowContext(fnType.In(0)) {
			return fmt.Errorf("expected first argument to be workflow.Context but found %s", fnType.In(0))
		}
	}

	// Return values
	// We expect either
	// 	<result>, error
	//	(or) just error
	if fnType.NumOut() < 1 || fnType.NumOut() > 2 {
		return fmt.Errorf(
			"expected function to return result, error or just error, but found %d return values", fnType.NumOut(),
		)
	}
	if fnType.NumOut() > 1 && !isValidResultType(fnType.Out(0)) {
		return fmt.Errorf(
			"expected function first return value to return valid type but found: %v", fnType.Out(0).Kind(),
		)
	}
	if !isError(fnType.Out(fnType.NumOut() - 1)) {
		return fmt.Errorf(
			"expected function second return value to return error but found %v", fnType.Out(fnType.NumOut()-1).Kind(),
		)
	}
	return nil
}

// encode multiple arguments(arguments to a function).
func encodeArgs(dc DataConverter, args []interface{}) (*commonpb.Payloads, error) {
	return dc.ToData(args...)
}

// decode multiple arguments(arguments to a function).
func decodeArgs(dc DataConverter, fnType reflect.Type, data *commonpb.Payloads) (result []reflect.Value, err error) {
	r, err := decodeArgsToValues(dc, fnType, data)
	if err != nil {
		return
	}
	for i := 0; i < len(r); i++ {
		result = append(result, reflect.ValueOf(r[i]).Elem())
	}
	return
}

func decodeArgsToValues(dc DataConverter, fnType reflect.Type, data *commonpb.Payloads) (result []interface{}, err error) {
argsLoop:
	for i := 0; i < fnType.NumIn(); i++ {
		argT := fnType.In(i)
		if i == 0 && (isActivityContext(argT) || isWorkflowContext(argT)) {
			continue argsLoop
		}
		arg := reflect.New(argT).Interface()
		result = append(result, arg)
	}
	err = dc.FromData(data, result...)
	if err != nil {
		return
	}
	return
}

// encode single value(like return parameter).
func encodeArg(dc DataConverter, arg interface{}) (*commonpb.Payloads, error) {
	return dc.ToData(arg)
}

// decode single value(like return parameter).
func decodeArg(dc DataConverter, data *commonpb.Payloads, to interface{}) error {
	return dc.FromData(data, to)
}

func decodeAndAssignValue(dc DataConverter, from interface{}, toValuePtr interface{}) error {
	if toValuePtr == nil {
		return nil
	}
	if rf := reflect.ValueOf(toValuePtr); rf.Type().Kind() != reflect.Ptr {
		return errors.New("value parameter provided is not a pointer")
	}
	if data, ok := from.(*commonpb.Payloads); ok {
		if err := decodeArg(dc, data, toValuePtr); err != nil {
			return err
		}
	} else if fv := reflect.ValueOf(from); fv.IsValid() {
		fromType := fv.Type()
		toType := reflect.TypeOf(toValuePtr).Elem()
		assignable := fromType.AssignableTo(toType)
		if !assignable {
			return fmt.Errorf("%s is not assignable to  %s", fromType.Name(), toType.Name())
		}
		reflect.ValueOf(toValuePtr).Elem().Set(fv)
	}
	return nil
}

func newRegistry() *registry {
	return &registry{
		workflowFuncMap:  make(map[string]interface{}),
		workflowAliasMap: make(map[string]string),
		activityFuncMap:  make(map[string]activity),
		activityAliasMap: make(map[string]string),
	}
}

// Wrapper to execute workflow functions.
type workflowExecutor struct {
	workflowType string
	fn           interface{}
	interceptors []WorkflowInterceptorFactory
}

func (we *workflowExecutor) Execute(ctx Context, input *commonpb.Payloads) (*commonpb.Payloads, error) {
	var args []interface{}
	dataConverter := getWorkflowEnvOptions(ctx).dataConverter
	fnType := reflect.TypeOf(we.fn)

	decoded, err := decodeArgsToValues(dataConverter, fnType, input)
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode the workflow function input payload with error: %w, function name: %v",
			err, we.workflowType)
	}
	args = append(args, decoded...)

	envInterceptor := getEnvInterceptor(ctx)
	envInterceptor.fn = we.fn
	results := envInterceptor.interceptorChainHead.ExecuteWorkflow(ctx, we.workflowType, args...)
	return serializeResults(we.fn, results, dataConverter)
}

// Wrapper to execute activity functions.
type activityExecutor struct {
	name string
	fn   interface{}
}

func (ae *activityExecutor) ActivityType() ActivityType {
	return ActivityType{Name: ae.name}
}

func (ae *activityExecutor) GetFunction() interface{} {
	return ae.fn
}

func (ae *activityExecutor) Execute(ctx context.Context, input *commonpb.Payloads) (*commonpb.Payloads, error) {
	fnType := reflect.TypeOf(ae.fn)
	var args []reflect.Value
	dataConverter := getDataConverterFromActivityCtx(ctx)

	// activities optionally might not take context.
	if fnType.NumIn() > 0 && isActivityContext(fnType.In(0)) {
		args = append(args, reflect.ValueOf(ctx))
	}

	decoded, err := decodeArgs(dataConverter, fnType, input)
	if err != nil {
		return nil, fmt.Errorf(
			"unable to decode the activity function input payload with error: %w for function name: %v",
			err, ae.name)
	}
	args = append(args, decoded...)

	fnValue := reflect.ValueOf(ae.fn)
	retValues := fnValue.Call(args)
	return validateFunctionAndGetResults(ae.fn, retValues, dataConverter)
}

func (ae *activityExecutor) ExecuteWithActualArgs(ctx context.Context, actualArgs []interface{}) (*commonpb.Payloads, error) {
	retValues := ae.executeWithActualArgsWithoutParseResult(ctx, actualArgs)
	dataConverter := getDataConverterFromActivityCtx(ctx)

	return validateFunctionAndGetResults(ae.fn, retValues, dataConverter)
}

func (ae *activityExecutor) executeWithActualArgsWithoutParseResult(ctx context.Context, actualArgs []interface{}) []reflect.Value {
	fnType := reflect.TypeOf(ae.fn)
	var args []reflect.Value

	// activities optionally might not take context.
	argsOffeset := 0
	if fnType.NumIn() > 0 && isActivityContext(fnType.In(0)) {
		args = append(args, reflect.ValueOf(ctx))
		argsOffeset = 1
	}

	for i, arg := range actualArgs {
		if arg == nil {
			args = append(args, reflect.New(fnType.In(i+argsOffeset)).Elem())
		} else {
			args = append(args, reflect.ValueOf(arg))
		}
	}

	fnValue := reflect.ValueOf(ae.fn)
	retValues := fnValue.Call(args)
	return retValues
}

func getDataConverterFromActivityCtx(ctx context.Context) DataConverter {
	if ctx == nil || ctx.Value(activityEnvContextKey) == nil {
		return getDefaultDataConverter()
	}
	info := ctx.Value(activityEnvContextKey).(*activityEnvironment)
	if info.dataConverter == nil {
		return getDefaultDataConverter()
	}
	return info.dataConverter
}

// AggregatedWorker combines management of both workflowWorker and activityWorker worker lifecycle.
type AggregatedWorker struct {
	workflowWorker *workflowWorker
	activityWorker *activityWorker
	sessionWorker  *sessionWorker
	logger         *zap.Logger
	registry       *registry
}

// RegisterWorkflow registers workflow implementation with the AggregatedWorker
func (aw *AggregatedWorker) RegisterWorkflow(w interface{}) {
	aw.registry.RegisterWorkflow(w)
}

// RegisterWorkflowWithOptions registers workflow implementation with the AggregatedWorker
func (aw *AggregatedWorker) RegisterWorkflowWithOptions(w interface{}, options RegisterWorkflowOptions) {
	aw.registry.RegisterWorkflowWithOptions(w, options)
}

// RegisterActivity registers activity implementation with the AggregatedWorker
func (aw *AggregatedWorker) RegisterActivity(a interface{}) {
	aw.registry.RegisterActivity(a)
}

// RegisterActivityWithOptions registers activity implementation with the AggregatedWorker
func (aw *AggregatedWorker) RegisterActivityWithOptions(a interface{}, options RegisterActivityOptions) {
	aw.registry.RegisterActivityWithOptions(a, options)
}

// Start starts the worker in a non-blocking fashion
func (aw *AggregatedWorker) Start() error {
	if err := initBinaryChecksum(); err != nil {
		return fmt.Errorf("failed to get executable checksum: %v", err)
	}

	if !isInterfaceNil(aw.workflowWorker) {
		if len(aw.registry.getRegisteredWorkflowTypes()) == 0 {
			aw.logger.Info("No workflows registered. Skipping workflow worker start")
		} else {
			if err := aw.workflowWorker.Start(); err != nil {
				return err
			}
		}
	}
	if !isInterfaceNil(aw.activityWorker) {
		if len(aw.registry.getRegisteredActivities()) == 0 {
			aw.logger.Info("No activities registered. Skipping activity worker start")
		} else {
			if err := aw.activityWorker.Start(); err != nil {
				// stop workflow worker.
				if !isInterfaceNil(aw.workflowWorker) && len(aw.registry.getRegisteredWorkflowTypes()) > 0 {
					aw.workflowWorker.Stop()
				}
				return err
			}
		}
	}

	if !isInterfaceNil(aw.sessionWorker) && len(aw.registry.getRegisteredActivities()) > 0 {
		aw.logger.Info("Starting session worker")
		if err := aw.sessionWorker.Start(); err != nil {
			// stop workflow worker and activity worker.
			if !isInterfaceNil(aw.workflowWorker) {
				aw.workflowWorker.Stop()
			}
			if !isInterfaceNil(aw.activityWorker) {
				aw.activityWorker.Stop()
			}
			return err
		}
	}

	aw.logger.Info("Started Worker")
	return nil
}

var binaryChecksum string
var binaryChecksumLock sync.Mutex

// SetBinaryChecksum sets the identifier of the binary(aka BinaryChecksum).
// The identifier is mainly used in recording reset points when respondDecisionTaskCompleted. For each workflow, the very first
// decision completed by a binary will be associated as a auto-reset point for the binary. So that when a customer wants to
// mark the binary as bad, the workflow will be reset to that point -- which means workflow will forget all progress generated
// by the binary.
// On another hand, once the binary is marked as bad, the bad binary cannot poll decision and make any progress any more.
func SetBinaryChecksum(checksum string) {
	binaryChecksumLock.Lock()
	defer binaryChecksumLock.Unlock()

	binaryChecksum = checksum
}

func initBinaryChecksum() error {
	binaryChecksumLock.Lock()
	defer binaryChecksumLock.Unlock()

	return initBinaryChecksumLocked()
}

// callers MUST hold binaryChecksumLock before calling
func initBinaryChecksumLocked() error {
	if len(binaryChecksum) > 0 {
		return nil
	}

	exec, err := os.Executable()
	if err != nil {
		return err
	}

	f, err := os.Open(exec)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close() // error is unimportant as it is read-only
	}()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	checksum := h.Sum(nil)
	binaryChecksum = hex.EncodeToString(checksum[:])

	return nil
}

func getBinaryChecksum() string {
	binaryChecksumLock.Lock()
	defer binaryChecksumLock.Unlock()

	if len(binaryChecksum) == 0 {
		err := initBinaryChecksumLocked()
		if err != nil {
			panic(err)
		}
	}

	return binaryChecksum
}

// Run is a blocking start and cleans up resources when killed
// returns error only if it fails to start the worker
func (aw *AggregatedWorker) Run() error {
	if err := aw.Start(); err != nil {
		return err
	}
	d := <-getKillSignal()
	aw.logger.Info("Worker has been killed", zap.String("Signal", d.String()))
	aw.Stop()
	return nil
}

// Stop cleans up any resources opened by worker
func (aw *AggregatedWorker) Stop() {
	if !isInterfaceNil(aw.workflowWorker) {
		aw.workflowWorker.Stop()
	}
	if !isInterfaceNil(aw.activityWorker) {
		aw.activityWorker.Stop()
	}
	if !isInterfaceNil(aw.sessionWorker) {
		aw.sessionWorker.Stop()
	}

	aw.logger.Info("Stopped Worker")
}

// WorkflowReplayer is used to replay workflow code from an event history
type WorkflowReplayer struct {
	registry *registry
}

// NewWorkflowReplayer creates an instance of the WorkflowReplayer
func NewWorkflowReplayer() *WorkflowReplayer {
	return &WorkflowReplayer{registry: newRegistry()}
}

// RegisterWorkflow registers workflow function to replay
func (aw *WorkflowReplayer) RegisterWorkflow(w interface{}) {
	aw.registry.RegisterWorkflow(w)
}

// RegisterWorkflowWithOptions registers workflow function with custom workflow name to replay
func (aw *WorkflowReplayer) RegisterWorkflowWithOptions(w interface{}, options RegisterWorkflowOptions) {
	aw.registry.RegisterWorkflowWithOptions(w, options)
}

// ReplayWorkflowHistory executes a single decision task for the given history.
// Use for testing the backwards compatibility of code changes and troubleshooting workflows in a debugger.
// The logger is an optional parameter. Defaults to the noop logger.
func (aw *WorkflowReplayer) ReplayWorkflowHistory(logger *zap.Logger, history *eventpb.History) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	testReporter := logger.Sugar()
	controller := gomock.NewController(testReporter)
	service := workflowservicemock.NewMockWorkflowServiceClient(controller)

	return aw.replayWorkflowHistory(logger, service, ReplayNamespace, history)
}

// ReplayWorkflowHistoryFromJSONFile executes a single decision task for the given json history file.
// Use for testing the backwards compatibility of code changes and troubleshooting workflows in a debugger.
// The logger is an optional parameter. Defaults to the noop logger.
func (aw *WorkflowReplayer) ReplayWorkflowHistoryFromJSONFile(logger *zap.Logger, jsonfileName string) error {
	return aw.ReplayPartialWorkflowHistoryFromJSONFile(logger, jsonfileName, 0)
}

// ReplayPartialWorkflowHistoryFromJSONFile executes a single decision task for the given json history file upto provided
// lastEventID(inclusive).
// Use for testing the backwards compatibility of code changes and troubleshooting workflows in a debugger.
// The logger is an optional parameter. Defaults to the noop logger.
func (aw *WorkflowReplayer) ReplayPartialWorkflowHistoryFromJSONFile(logger *zap.Logger, jsonfileName string, lastEventID int64) error {
	history, err := extractHistoryFromFile(jsonfileName, lastEventID)

	if err != nil {
		return err
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	testReporter := logger.Sugar()
	controller := gomock.NewController(testReporter)
	service := workflowservicemock.NewMockWorkflowServiceClient(controller)

	return aw.replayWorkflowHistory(logger, service, ReplayNamespace, history)
}

// ReplayWorkflowExecution replays workflow execution loading it from Temporal service.
func (aw *WorkflowReplayer) ReplayWorkflowExecution(ctx context.Context, service workflowservice.WorkflowServiceClient, logger *zap.Logger, namespace string, execution WorkflowExecution) error {
	sharedExecution := &commonpb.WorkflowExecution{
		RunId:      execution.RunID,
		WorkflowId: execution.ID,
	}
	request := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: namespace,
		Execution: sharedExecution,
	}
	hResponse, err := service.GetWorkflowExecutionHistory(ctx, request)
	if err != nil {
		return err
	}

	if hResponse.RawHistory != nil {
		history, err := serializer.DeserializeBlobDataToHistoryEvents(hResponse.RawHistory, filterpb.HistoryEventFilterType_AllEvent)
		if err != nil {
			return err
		}

		hResponse.History = history
	}

	return aw.replayWorkflowHistory(logger, service, namespace, hResponse.History)
}

func (aw *WorkflowReplayer) replayWorkflowHistory(logger *zap.Logger, service workflowservice.WorkflowServiceClient, namespace string, history *eventpb.History) error {
	taskList := "ReplayTaskList"
	events := history.Events
	if events == nil {
		return errors.New("empty events")
	}
	if len(events) < 3 {
		return errors.New("at least 3 events expected in the history")
	}
	first := events[0]
	if first.GetEventType() != eventpb.EventType_WorkflowExecutionStarted {
		return errors.New("first event is not WorkflowExecutionStarted")
	}
	last := events[len(events)-1]

	attr := first.GetWorkflowExecutionStartedEventAttributes()
	if attr == nil {
		return errors.New("corrupted WorkflowExecutionStarted")
	}
	workflowType := attr.WorkflowType
	execution := &commonpb.WorkflowExecution{
		RunId:      uuid.NewRandom().String(),
		WorkflowId: "ReplayId",
	}
	if first.GetWorkflowExecutionStartedEventAttributes().GetOriginalExecutionRunId() != "" {
		execution.RunId = first.GetWorkflowExecutionStartedEventAttributes().GetOriginalExecutionRunId()
	}

	task := &workflowservice.PollForDecisionTaskResponse{
		Attempt:                0,
		TaskToken:              []byte("ReplayTaskToken"),
		WorkflowType:           workflowType,
		WorkflowExecution:      execution,
		History:                history,
		PreviousStartedEventId: math.MaxInt64,
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	metricScope := tally.NoopScope
	iterator := &historyIteratorImpl{
		nextPageToken: task.NextPageToken,
		execution:     task.WorkflowExecution,
		namespace:     ReplayNamespace,
		service:       service,
		metricsScope:  metricScope,
		maxEventID:    task.GetStartedEventId(),
	}
	params := workerExecutionParameters{
		Namespace: namespace,
		TaskList:  taskList,
		Identity:  "replayID",
		Logger:    logger,
	}
	taskHandler := newWorkflowTaskHandler(params, nil, aw.registry)
	resp, err := taskHandler.ProcessWorkflowTask(&workflowTask{task: task, historyIterator: iterator}, nil)
	if err != nil {
		return err
	}

	if last.GetEventType() != eventpb.EventType_WorkflowExecutionCompleted && last.GetEventType() != eventpb.EventType_WorkflowExecutionContinuedAsNew {
		return nil
	}

	if resp != nil {
		completeReq, ok := resp.(*workflowservice.RespondDecisionTaskCompletedRequest)
		if ok {
			for _, d := range completeReq.Decisions {
				if d.GetDecisionType() == decisionpb.DecisionType_ContinueAsNewWorkflowExecution {
					if last.GetEventType() == eventpb.EventType_WorkflowExecutionContinuedAsNew {
						inputA := d.GetContinueAsNewWorkflowExecutionDecisionAttributes().GetInput()
						inputB := last.GetWorkflowExecutionContinuedAsNewEventAttributes().GetInput()
						if proto.Equal(inputA, inputB) {
							return nil
						}
					}
				}
				if d.GetDecisionType() == decisionpb.DecisionType_CompleteWorkflowExecution {
					if last.GetEventType() == eventpb.EventType_WorkflowExecutionCompleted {
						resultA := last.GetWorkflowExecutionCompletedEventAttributes().GetResult()
						resultB := d.GetCompleteWorkflowExecutionDecisionAttributes().GetResult()
						if proto.Equal(resultA, resultB) {
							return nil
						}
					}
				}
			}
		}
	}
	return fmt.Errorf("replay workflow doesn't return the same result as the last event, resp: %v, last: %v", resp, last)
}

func extractHistoryFromFile(jsonfileName string, lastEventID int64) (*eventpb.History, error) {
	reader, err := os.Open(jsonfileName)
	if err != nil {
		return nil, err
	}

	var deserializedHistory eventpb.History
	err = jsonpb.Unmarshal(reader, &deserializedHistory)

	if err != nil {
		return nil, err
	}

	if lastEventID <= 0 {
		return &deserializedHistory, nil
	}

	// Caller is potentially asking for subset of history instead of all history events
	var events []*eventpb.HistoryEvent
	for _, event := range deserializedHistory.Events {
		events = append(events, event)
		if event.GetEventId() == lastEventID {
			// Copy history up to last event (inclusive)
			break
		}
	}
	history := &eventpb.History{Events: events}

	return history, nil
}

// NewAggregatedWorker returns an instance to manage both activity and decision workers
func NewAggregatedWorker(client *WorkflowClient, taskList string, options WorkerOptions) *AggregatedWorker {
	setClientDefaults(client)
	setWorkerOptionsDefaults(&options)
	ctx := options.BackgroundActivityContext
	if ctx == nil {
		ctx = context.Background()
	}
	backgroundActivityContext, backgroundActivityContextCancel := context.WithCancel(ctx)

	workerParams := workerExecutionParameters{
		Namespace:                            client.namespace,
		TaskList:                             taskList,
		ConcurrentActivityExecutionSize:      options.MaxConcurrentActivityExecutionSize,
		WorkerActivitiesPerSecond:            options.WorkerActivitiesPerSecond,
		MaxConcurrentActivityPollers:         options.MaxConcurrentActivityTaskPollers,
		ConcurrentLocalActivityExecutionSize: options.MaxConcurrentLocalActivityExecutionSize,
		WorkerLocalActivitiesPerSecond:       options.WorkerLocalActivitiesPerSecond,
		ConcurrentDecisionTaskExecutionSize:  options.MaxConcurrentDecisionTaskExecutionSize,
		WorkerDecisionTasksPerSecond:         options.WorkerDecisionTasksPerSecond,
		MaxConcurrentDecisionPollers:         options.MaxConcurrentDecisionTaskPollers,
		Identity:                             client.identity,
		MetricsScope:                         client.metricsScope,
		Logger:                               client.logger,
		EnableLoggingInReplay:                options.EnableLoggingInReplay,
		UserContext:                          backgroundActivityContext,
		UserContextCancel:                    backgroundActivityContextCancel,
		DisableStickyExecution:               options.DisableStickyExecution,
		StickyScheduleToStartTimeout:         options.StickyScheduleToStartTimeout,
		TaskListActivitiesPerSecond:          options.TaskListActivitiesPerSecond,
		WorkflowPanicPolicy:                  options.NonDeterministicWorkflowPolicy,
		DataConverter:                        client.dataConverter,
		WorkerStopTimeout:                    options.WorkerStopTimeout,
		ContextPropagators:                   client.contextPropagators,
		Tracer:                               client.tracer,
	}

	ensureRequiredParams(&workerParams)
	workerParams.Logger = workerParams.Logger.With(
		zapcore.Field{Key: tagNamespace, Type: zapcore.StringType, String: client.namespace},
		zapcore.Field{Key: tagTaskList, Type: zapcore.StringType, String: taskList},
		zapcore.Field{Key: tagWorkerID, Type: zapcore.StringType, String: workerParams.Identity},
	)
	logger := workerParams.Logger

	processTestTags(&options, &workerParams)

	// worker specific registry
	registry := newRegistry()
	registry.SetWorkflowInterceptors(options.WorkflowInterceptorChainFactories)

	// workflow factory.
	var workflowWorker *workflowWorker
	if !options.DisableWorkflowWorker {
		testTags := getTestTags(options.BackgroundActivityContext)
		if len(testTags) > 0 {
			workflowWorker = newWorkflowWorkerWithPressurePoints(client.workflowService, workerParams, testTags, registry)
		} else {
			workflowWorker = newWorkflowWorker(client.workflowService, workerParams, nil, registry)
		}
	}

	// activity types.
	var activityWorker *activityWorker

	if !options.DisableActivityWorker {
		activityWorker = newActivityWorker(client.workflowService, workerParams, nil, registry, nil)
	}

	var sessionWorker *sessionWorker
	if options.EnableSessionWorker {
		sessionWorker = newSessionWorker(client.workflowService, workerParams, nil, registry, options.MaxConcurrentSessionExecutionSize)
		registry.RegisterActivityWithOptions(sessionCreationActivity, RegisterActivityOptions{
			Name: sessionCreationActivityName,
		})
		registry.RegisterActivityWithOptions(sessionCompletionActivity, RegisterActivityOptions{
			Name: sessionCompletionActivityName,
		})

	}

	return &AggregatedWorker{
		workflowWorker: workflowWorker,
		activityWorker: activityWorker,
		sessionWorker:  sessionWorker,
		logger:         logger,
		registry:       registry,
	}
}

// tagScope with one or multiple tags, like
// tagScope(scope, tag1, val1, tag2, val2)
func tagScope(metricsScope tally.Scope, keyValuePairs ...string) tally.Scope {
	if metricsScope == nil {
		metricsScope = tally.NoopScope
	}
	if len(keyValuePairs)%2 != 0 {
		panic("tagScope key value are not in pairs")
	}
	tagsMap := map[string]string{}
	for i := 0; i < len(keyValuePairs); i += 2 {
		tagsMap[keyValuePairs[i]] = keyValuePairs[i+1]
	}
	return metricsScope.Tagged(tagsMap)
}

func processTestTags(wOptions *WorkerOptions, ep *workerExecutionParameters) {
	testTags := getTestTags(wOptions.BackgroundActivityContext)
	if testTags != nil {
		if paramsOverride, ok := testTags[workerOptionsConfig]; ok {
			for key, val := range paramsOverride {
				switch key {
				case workerOptionsConfigConcurrentPollRoutineSize:
					if size, err := strconv.Atoi(val); err == nil {
						ep.MaxConcurrentActivityPollers = size
						ep.MaxConcurrentDecisionPollers = size
					}
				}
			}
		}
	}
}

func isWorkflowContext(inType reflect.Type) bool {
	// NOTE: We don't expect any one to derive from workflow context.
	return inType == reflect.TypeOf((*Context)(nil)).Elem()
}

func isValidResultType(inType reflect.Type) bool {
	// https://golang.org/pkg/reflect/#Kind
	switch inType.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return false
	}

	return true
}

func isError(inType reflect.Type) bool {
	errorElem := reflect.TypeOf((*error)(nil)).Elem()
	return inType != nil && inType.Implements(errorElem)
}

func getFunctionName(i interface{}) string {
	if fullName, ok := i.(string); ok {
		return fullName
	}
	fullName := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	elements := strings.Split(fullName, ".")
	shortName := elements[len(elements)-1]
	// This allows to call activities by method pointer
	// Compiler adds -fm suffix to a function name which has a receiver
	// Note that this works even if struct pointer used to get the function is nil
	// It is possible because nil receivers are allowed.
	// For example:
	// var a *Activities
	// ExecuteActivity(ctx, a.Foo)
	// will call this function which is going to return "Foo"
	return strings.TrimSuffix(shortName, "-fm")
}

func getActivityFunctionName(r *registry, i interface{}) string {
	result := getFunctionName(i)
	if alias, ok := r.getActivityAlias(result); ok {
		result = alias
	}
	return result
}

func getWorkflowFunctionName(r *registry, i interface{}) string {
	result := getFunctionName(i)
	if alias, ok := r.getWorkflowAlias(result); ok {
		result = alias
	}
	return result
}

func isInterfaceNil(i interface{}) bool {
	return i == nil || reflect.ValueOf(i).IsNil()
}

func getReadOnlyChannel(c chan struct{}) <-chan struct{} {
	return c
}

func setWorkerOptionsDefaults(options *WorkerOptions) {
	if options.MaxConcurrentActivityExecutionSize == 0 {
		options.MaxConcurrentActivityExecutionSize = defaultMaxConcurrentActivityExecutionSize
	}
	if options.WorkerActivitiesPerSecond == 0 {
		options.WorkerActivitiesPerSecond = defaultWorkerActivitiesPerSecond
	}
	if options.MaxConcurrentActivityTaskPollers <= 0 {
		options.MaxConcurrentActivityTaskPollers = defaultConcurrentPollRoutineSize
	}
	if options.MaxConcurrentDecisionTaskExecutionSize == 0 {
		options.MaxConcurrentDecisionTaskExecutionSize = defaultMaxConcurrentTaskExecutionSize
	}
	if options.WorkerDecisionTasksPerSecond == 0 {
		options.WorkerDecisionTasksPerSecond = defaultWorkerTaskExecutionRate
	}
	if options.MaxConcurrentDecisionTaskPollers <= 0 {
		options.MaxConcurrentDecisionTaskPollers = defaultConcurrentPollRoutineSize
	}
	if options.MaxConcurrentLocalActivityExecutionSize == 0 {
		options.MaxConcurrentLocalActivityExecutionSize = defaultMaxConcurrentLocalActivityExecutionSize
	}
	if options.WorkerLocalActivitiesPerSecond == 0 {
		options.WorkerLocalActivitiesPerSecond = defaultWorkerLocalActivitiesPerSecond
	}
	if options.TaskListActivitiesPerSecond == 0 {
		options.TaskListActivitiesPerSecond = defaultTaskListActivitiesPerSecond
	}
	if options.StickyScheduleToStartTimeout.Seconds() == 0 {
		options.StickyScheduleToStartTimeout = stickyDecisionScheduleToStartTimeoutSeconds * time.Second
	}
	if options.MaxConcurrentSessionExecutionSize == 0 {
		options.MaxConcurrentSessionExecutionSize = defaultMaxConcurrentSessionExecutionSize
	}
}

// setClientDefaults should be needed only in unit tests.
func setClientDefaults(client *WorkflowClient) {
	if client.dataConverter == nil {
		client.dataConverter = getDefaultDataConverter()
	}
	if client.namespace == "" {
		client.namespace = DefaultNamespace
	}
	if client.tracer == nil {
		client.tracer = opentracing.NoopTracer{}
	}
	if client.metricsScope == nil {
		client.metricsScope = metrics.NewTaggedScope(nil)
	}
}

// getTestTags returns the test tags in the context.
func getTestTags(ctx context.Context) map[string]map[string]string {
	if ctx != nil {
		env := ctx.Value(testTagsContextKey)
		if env != nil {
			return env.(map[string]map[string]string)
		}
	}
	return nil
}
