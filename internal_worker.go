// Copyright (c) 2017 Uber Technologies, Inc.
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

package cadence

// All code in this file is private to the package.

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/uber-go/tally"
	m "go.uber.org/cadence/.gen/go/cadence"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/common"
	"go.uber.org/cadence/common/backoff"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// Set to 2 pollers for now, can adjust later if needed. The typical RTT (round-trip time) is below 1ms within data
	// center. And the poll API latency is about 5ms. With 2 poller, we could achieve around 300~400 RPS.
	defaultConcurrentPollRoutineSize = 2

	defaultMaxConcurrentActivityExecutionSize = 1000   // Large concurrent activity execution size (1k)
	defaultMaxActivityExecutionRate           = 100000 // Large activity execution rate (unlimited)

	defaultMaxConcurrentWorkflowExecutionSize = 50     // hardcoded max workflow execution size.
	defaultMaxWorkflowExecutionRate           = 100000 // Large workflow execution rate (unlimited)
)

// Assert that structs do indeed implement the interfaces
var _ Worker = (*aggregatedWorker)(nil)

type (

	// WorkflowFactory function is used to create a workflow implementation object.
	// It is needed as a workflow object is created on every decision.
	// To start a workflow instance use NewClient(...).StartWorkflow(...)
	workflowFactory func(workflowType WorkflowType) (workflow, error)

	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	workflowWorker struct {
		executionParameters workerExecutionParameters
		workflowService     m.TChanWorkflowService
		domain              string
		poller              taskPoller // taskPoller to poll and process the tasks.
		worker              *baseWorker
		identity            string
	}

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	activityWorker struct {
		executionParameters workerExecutionParameters
		workflowService     m.TChanWorkflowService
		domain              string
		poller              taskPoller
		worker              *baseWorker
		identity            string
	}

	// Worker overrides.
	workerOverrides struct {
		workflowTaskHandler WorkflowTaskHandler
		activityTaskHandler ActivityTaskHandler
	}

	// workerExecutionParameters defines worker configure/execution options.
	workerExecutionParameters struct {
		// Task list name to poll.
		TaskList string

		// Defines how many concurrent poll requests for the task list by this worker.
		ConcurrentPollRoutineSize int

		// Defines how many concurrent executions for task list by this worker.
		ConcurrentActivityExecutionSize int

		// Defines rate limiting on number of activity tasks that can be executed per MaxActivityExecutionRateRefreshDuration.
		MaxActivityExecutionRate int

		// Defines refresh duration for rate limit. If not specified, it uses 1s as default.
		MaxActivityExecutionRateRefreshDuration time.Duration

		// User can provide an identity for the debuggability. If not provided the framework has
		// a default option.
		Identity string

		MetricsScope tally.Scope

		Logger *zap.Logger

		// Enable logging in replay mode
		EnableLoggingInReplay bool

		// Context to store user provided key/value pairs
		UserContext context.Context
	}
)

// newWorkflowWorker returns an instance of the workflow worker.
func newWorkflowWorker(
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	ppMgr pressurePointMgr,
	hostEnv *hostEnvImpl,
) Worker {
	return newWorkflowWorkerInternal(service, domain, params, ppMgr, nil, hostEnv)
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
		//config.Level.SetLevel(zapcore.DebugLevel)
		logger, _ := config.Build()
		params.Logger = logger
		params.Logger.Info("No logger configured for cadence worker. Created default one.")
	}

	if params.MetricsScope == nil {
		params.MetricsScope = tally.NoopScope
		params.Logger.Info("No metrics scope configured for cadence worker. Use NoopScope as default.")
	}
}

// verifyDomainExist does a DescribeDomain operation on the specified domain with backoff/retry
// It returns an error, if the server returns an EntityNotExist or BadRequest error
// On any other transient error, this method will just return success
func verifyDomainExist(client m.TChanWorkflowService, domain string, logger *zap.Logger) error {

	descDomainOp := func() error {
		ctx, cancel := newTChannelContext()
		defer cancel()
		_, err := client.DescribeDomain(ctx, &shared.DescribeDomainRequest{Name: &domain})
		if err != nil {
			if _, ok := err.(*shared.EntityNotExistsError); ok {
				logger.Error("domain does not exist", zap.String("domain", domain), zap.Error(err))
				return err
			}
			if _, ok := err.(*shared.BadRequestError); ok {
				logger.Error("domain does not exist", zap.String("domain", domain), zap.Error(err))
				return err
			}
			// on any other error, just return true
			logger.Warn("unable to verify if domain exist", zap.String("domain", domain), zap.Error(err))
		}
		return nil
	}

	if len(domain) == 0 {
		return errors.New("domain cannot be empty")
	}

	// exponential backoff retry for upto a minute
	return backoff.Retry(descDomainOp, serviceOperationRetryPolicy, isServiceTransientError)
}

func newWorkflowWorkerInternal(
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	ppMgr pressurePointMgr,
	overrides *workerOverrides,
	hostEnv *hostEnvImpl,
) Worker {
	// Get a workflow task handler.
	ensureRequiredParams(&params)
	var taskHandler WorkflowTaskHandler
	if overrides != nil && overrides.workflowTaskHandler != nil {
		taskHandler = overrides.workflowTaskHandler
	} else {
		taskHandler = newWorkflowTaskHandler(domain, params, ppMgr, hostEnv)
	}
	return newWorkflowTaskWorkerInternal(taskHandler, service, domain, params)
}

func newWorkflowTaskWorkerInternal(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
) Worker {
	ensureRequiredParams(&params)
	poller := newWorkflowTaskPoller(
		taskHandler,
		service,
		domain,
		params,
	)
	worker := newBaseWorker(baseWorkerOptions{
		pollerCount:                params.ConcurrentPollRoutineSize,
		maxConcurrentTask:          defaultMaxConcurrentWorkflowExecutionSize,
		maxTaskRate:                defaultMaxWorkflowExecutionRate,
		maxTaskRateRefreshDuration: time.Second,
		taskWorker:                 poller,
		identity:                   params.Identity,
		workerType:                 "DecisionWorker"},
		params.Logger,
		params.MetricsScope,
	)

	return &workflowWorker{
		executionParameters: params,
		workflowService:     service,
		poller:              poller,
		worker:              worker,
		identity:            params.Identity,
		domain:              domain,
	}
}

// Start the worker.
func (ww *workflowWorker) Start() error {
	err := verifyDomainExist(ww.workflowService, ww.domain, ww.worker.logger)
	if err != nil {
		return err
	}
	ww.worker.Start()
	return nil // TODO: propagate error
}

func (ww *workflowWorker) Run() error {
	err := verifyDomainExist(ww.workflowService, ww.domain, ww.worker.logger)
	if err != nil {
		return err
	}
	ww.worker.Run()
	return nil
}

// Shutdown the worker.
func (ww *workflowWorker) Stop() {
	ww.worker.Stop()
}

func newActivityWorker(
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	overrides *workerOverrides,
	env *hostEnvImpl,
) Worker {
	ensureRequiredParams(&params)
	// Get a activity task handler.
	var taskHandler ActivityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(service, params, env)
	}
	return newActivityTaskWorker(taskHandler, service, domain, params)
}

func newActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	workerParams workerExecutionParameters,
) (worker Worker) {
	ensureRequiredParams(&workerParams)

	poller := newActivityTaskPoller(
		taskHandler,
		service,
		domain,
		workerParams,
	)

	base := newBaseWorker(
		baseWorkerOptions{
			pollerCount:                workerParams.ConcurrentPollRoutineSize,
			maxConcurrentTask:          workerParams.ConcurrentActivityExecutionSize,
			maxTaskRate:                workerParams.MaxActivityExecutionRate,
			maxTaskRateRefreshDuration: workerParams.MaxActivityExecutionRateRefreshDuration,
			taskWorker:                 poller,
			identity:                   workerParams.Identity,
			workerType:                 "ActivityWorker",
		},
		workerParams.Logger,
		workerParams.MetricsScope,
	)

	return &activityWorker{
		executionParameters: workerParams,
		workflowService:     service,
		worker:              base,
		poller:              poller,
		identity:            workerParams.Identity,
		domain:              domain,
	}
}

// Start the worker.
func (aw *activityWorker) Start() error {
	err := verifyDomainExist(aw.workflowService, aw.domain, aw.worker.logger)
	if err != nil {
		return err
	}
	aw.worker.Start()
	return nil // TODO: propagate errors
}

// Run the worker.
func (aw *activityWorker) Run() error {
	err := verifyDomainExist(aw.workflowService, aw.domain, aw.worker.logger)
	if err != nil {
		return err
	}
	aw.worker.Run()
	return nil
}

// Shutdown the worker.
func (aw *activityWorker) Stop() {
	aw.worker.Stop()
}

type workerFunc func(ctx Context, input []byte) ([]byte, error)
type activityFunc func(ctx context.Context, input []byte) ([]byte, error)

type interceptorFn func(name string, workflow interface{}) (string, interface{})

// hostEnvImpl is the implementation of hostEnv
type hostEnvImpl struct {
	sync.Mutex
	workflowFuncMap                  map[string]interface{}
	workflowAliasMap                 map[string]string
	activityFuncMap                  map[string]activity
	activityAliasMap                 map[string]string
	encoding                         gobEncoding
	tEncoding                        thriftEncoding
	activityRegistrationInterceptors []interceptorFn
	workflowRegistrationInterceptors []interceptorFn
}

func (th *hostEnvImpl) AddWorkflowRegistrationInterceptor(i interceptorFn) {
	// As this function as well as registrations are called from init
	// the order is not defined. So this code deals with registration before listener is
	// registered as well as ones that come after.
	// This is also the reason that listener cannot reject registration as it can be applied
	// to already registered functions.
	th.Lock()
	funcMapCopy := th.workflowFuncMap // used to call listener outside of the lock.
	th.workflowRegistrationInterceptors = append(th.workflowRegistrationInterceptors, i)
	th.workflowFuncMap = make(map[string]interface{}) // clear map
	th.Unlock()
	for w, f := range funcMapCopy {
		intw, intf := i(w, f)
		th.Lock()
		th.workflowFuncMap[intw] = intf
		th.Unlock()
	}
}

func (th *hostEnvImpl) AddActivityRegistrationInterceptor(i interceptorFn) {
	// As this function as well as registrations are called from init
	// the order is not defined. So this code deals with registration before listener is
	// registered as well as ones that come after.
	// This is also the reason that listener cannot reject registration as it can be applied
	// to already registered functions.
	th.Lock()
	funcMapCopy := th.activityFuncMap // used to call listener outside of the lock.
	th.activityRegistrationInterceptors = append(th.activityRegistrationInterceptors, i)
	th.activityFuncMap = make(map[string]activity) // clear map
	th.Unlock()
	for w, a := range funcMapCopy {
		intw, intf := i(w, a.GetFunction())
		th.addActivity(intw, &activityExecutor{intw, intf})
	}
}

func (th *hostEnvImpl) RegisterWorkflow(af interface{}) error {
	return th.RegisterWorkflowWithOptions(af, RegisterWorkflowOptions{})
}

func (th *hostEnvImpl) RegisterWorkflowWithOptions(
	af interface{},
	options RegisterWorkflowOptions,
) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := validateFnFormat(fnType, true); err != nil {
		return err
	}
	fnName := getFunctionName(af)
	alias := options.Name
	registerName := fnName
	if len(alias) > 0 {
		registerName = alias
	}
	// Check if already registered
	if _, ok := th.getWorkflowFn(registerName); ok {
		return fmt.Errorf("workflow name \"%v\" is already registered", registerName)
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return err
	}
	registerName, af = th.invokeInterceptors(registerName, af, th.workflowRegistrationInterceptors)
	th.addWorkflowFn(registerName, af)
	if len(alias) > 0 {
		th.addWorkflowAlias(fnName, alias)
	}
	return nil
}

func (th *hostEnvImpl) RegisterActivity(af interface{}) error {
	return th.RegisterActivityWithOptions(af, RegisterActivityOptions{})
}

func (th *hostEnvImpl) RegisterActivityWithOptions(
	af interface{},
	options RegisterActivityOptions,
) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := validateFnFormat(fnType, false); err != nil {
		return err
	}
	fnName := getFunctionName(af)
	alias := options.Name
	registerName := fnName
	if len(alias) > 0 {
		registerName = alias
	}
	// Check if already registered
	if _, ok := th.getActivityFn(registerName); ok {
		return fmt.Errorf("activity type \"%v\" is already registered", registerName)
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return err
	}
	registerName, af = th.invokeInterceptors(registerName, af, th.activityRegistrationInterceptors)
	th.addActivityFn(registerName, af)
	if len(alias) > 0 {
		th.addActivityAlias(fnName, alias)
	}
	return nil
}

func (th *hostEnvImpl) invokeInterceptors(name string, f interface{}, interceptors []interceptorFn) (string, interface{}) {
	th.Lock()
	var copy []interceptorFn
	for _, i := range interceptors {
		copy = append(copy, i)
	}
	th.Unlock()
	for _, l := range copy {
		name, f = l(name, f)
	}
	return name, f
}

// Get the encoder.
func (th *hostEnvImpl) Encoder() encoding {
	return th.encoding
}

// Get thrift encoder.
func (th *hostEnvImpl) ThriftEncoder() encoding {
	return th.tEncoding
}

// Register all function args and return types with encoder.
func (th *hostEnvImpl) RegisterFnType(fnType reflect.Type) error {
	return th.registerEncodingTypes(fnType)
}

func (th *hostEnvImpl) addWorkflowAlias(fnName string, alias string) {
	th.Lock()
	defer th.Unlock()
	th.workflowAliasMap[fnName] = alias
}

func (th *hostEnvImpl) getWorkflowAlias(fnName string) (string, bool) {
	th.Lock()
	defer th.Unlock()
	alias, ok := th.workflowAliasMap[fnName]
	return alias, ok
}

func (th *hostEnvImpl) addWorkflowFn(fnName string, wf interface{}) {
	th.Lock()
	defer th.Unlock()
	th.workflowFuncMap[fnName] = wf
}

func (th *hostEnvImpl) getWorkflowFn(fnName string) (interface{}, bool) {
	th.Lock()
	defer th.Unlock()
	fn, ok := th.workflowFuncMap[fnName]
	return fn, ok
}

func (th *hostEnvImpl) getRegisteredWorkflowTypes() []string {
	th.Lock()
	defer th.Unlock()
	var r []string
	for t := range th.workflowFuncMap {
		r = append(r, t)
	}
	return r
}

func (th *hostEnvImpl) lenWorkflowFns() int {
	th.Lock()
	defer th.Unlock()
	return len(th.workflowFuncMap)
}

func (th *hostEnvImpl) lookupActivity(fnName string) (activity, bool) {
	lookup := fnName
	if alias, ok := th.getActivityAlias(fnName); ok {
		lookup = alias
	}
	return th.getActivity(lookup)
}

func (th *hostEnvImpl) addActivityAlias(fnName string, alias string) {
	th.Lock()
	defer th.Unlock()
	th.activityAliasMap[fnName] = alias
}

func (th *hostEnvImpl) getActivityAlias(fnName string) (string, bool) {
	th.Lock()
	defer th.Unlock()
	alias, ok := th.activityAliasMap[fnName]
	return alias, ok
}

func (th *hostEnvImpl) addActivity(fnName string, a activity) {
	th.Lock()
	defer th.Unlock()
	th.activityFuncMap[fnName] = a
}

func (th *hostEnvImpl) addActivityFn(fnName string, af interface{}) {
	th.addActivity(fnName, &activityExecutor{fnName, af})
}

func (th *hostEnvImpl) getActivity(fnName string) (activity, bool) {
	th.Lock()
	defer th.Unlock()
	a, ok := th.activityFuncMap[fnName]
	return a, ok
}

func (th *hostEnvImpl) getActivityFn(fnName string) (interface{}, bool) {
	if a, ok := th.getActivity(fnName); ok {
		return a.GetFunction(), ok
	}
	return nil, false
}

func (th *hostEnvImpl) getRegisteredActivities() []activity {
	activities := make([]activity, 0, len(th.activityFuncMap))
	for _, a := range th.activityFuncMap {
		activities = append(activities, a)
	}
	return activities
}

func (th *hostEnvImpl) getRegisteredActivityTypes() []string {
	th.Lock()
	defer th.Unlock()
	var r []string
	for t := range th.activityFuncMap {
		r = append(r, t)
	}
	return r
}

// register all the types with encoder.
func (th *hostEnvImpl) registerEncodingTypes(fnType reflect.Type) error {
	th.Lock()
	defer th.Unlock()

	// Register arguments.
	for i := 0; i < fnType.NumIn(); i++ {
		err := th.registerType(fnType.In(i), th.Encoder())
		if err != nil {
			return err
		}
	}
	// Register return types.
	// TODO: We need register all concrete implementations of error, Either
	// through pre-registry (or) at the time conversion.
	for i := 0; i < fnType.NumOut(); i++ {
		err := th.registerType(fnType.Out(i), th.Encoder())
		if err != nil {
			return err
		}
	}

	return nil
}

func (th *hostEnvImpl) registerValue(v interface{}, encoder encoding) error {
	if val := reflect.ValueOf(v); val.IsValid() {
		if val.Kind() == reflect.Ptr && val.IsNil() {
			return nil
		}
		rType := reflect.Indirect(val).Type()
		return th.registerType(rType, encoder)
	}
	return nil
}

// register type with our encoder.
func (th *hostEnvImpl) registerType(t reflect.Type, encoder encoding) error {
	// Interfaces cannot be registered, their implementations should be
	// https://golang.org/pkg/encoding/gob/#Register
	if t.Kind() == reflect.Interface || t.Kind() == reflect.Ptr {
		return nil
	}
	arg := reflect.Zero(t).Interface()
	return encoder.Register(arg)
}

func (th *hostEnvImpl) isUseThriftEncoding(objs []interface{}) bool {
	// NOTE: our criteria to use which encoder is simple if all the types are serializable using thrift then we use
	// thrift encoder. For everything else we default to gob.

	if len(objs) == 0 {
		return false
	}

	for i := 0; i < len(objs); i++ {
		if !isThriftType(objs[i]) {
			return false
		}
	}
	return true
}

func (th *hostEnvImpl) isUseThriftDecoding(objs []interface{}) bool {
	// NOTE: our criteria to use which encoder is simple if all the types are de-serializable using thrift then we use
	// thrift decoder. For everything else we default to gob.

	if len(objs) == 0 {
		return false
	}

	for i := 0; i < len(objs); i++ {
		rVal := reflect.ValueOf(objs[i])
		if rVal.Kind() != reflect.Ptr || !isThriftType(reflect.Indirect(rVal).Interface()) {
			return false
		}
	}
	return true
}

func (th *hostEnvImpl) getWorkflowDefinition(wt WorkflowType) (workflowDefinition, error) {
	lookup := wt.Name
	if alias, ok := th.getWorkflowAlias(lookup); ok {
		lookup = alias
	}
	wf, ok := th.getWorkflowFn(lookup)
	if !ok {
		supported := strings.Join(th.getRegisteredWorkflowTypes(), ", ")
		return nil, fmt.Errorf("Unable to find workflow type: %v. Supported types: [%v]", lookup, supported)
	}
	wd := &workflowExecutor{name: lookup, fn: wf}
	return newWorkflowDefinition(wd), nil
}

// Validate function parameters.
func validateFnFormat(fnType reflect.Type, isWorkflow bool) error {
	if fnType.Kind() != reflect.Func {
		return fmt.Errorf("expected a func as input but was %s", fnType.Kind())
	}
	if isWorkflow {
		if fnType.NumIn() < 1 {
			return fmt.Errorf(
				"expected at least one argument of type cadence.Context in function, found %d input arguments",
				fnType.NumIn(),
			)
		}
		if !isWorkflowContext(fnType.In(0)) {
			return fmt.Errorf("expected first argument to be cadence.Context but found %s", fnType.In(0))
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

// encode set of values.
func (th *hostEnvImpl) encode(r []interface{}) ([]byte, error) {
	if len(r) == 1 && isTypeByteSlice(reflect.TypeOf(r[0])) {
		return r[0].([]byte), nil
	}

	var encoder encoding
	if th.isUseThriftEncoding(r) {
		encoder = th.ThriftEncoder()
	} else {
		encoder = th.Encoder()
	}

	for _, v := range r {
		err := th.registerValue(v, encoder)
		if err != nil {
			return nil, err
		}
	}

	data, err := encoder.Marshal(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// decode a set of values.
func (th *hostEnvImpl) decode(data []byte, to []interface{}) error {
	if len(to) == 1 && isTypeByteSlice(reflect.TypeOf(to[0])) {
		reflect.ValueOf(to[0]).Elem().SetBytes(data)
		return nil
	}

	var encoder encoding
	if th.isUseThriftDecoding(to) {
		encoder = th.ThriftEncoder()
	} else {
		encoder = th.Encoder()
	}

	for _, v := range to {
		err := th.registerValue(v, encoder)
		if err != nil {
			return err
		}
	}

	if err := encoder.Unmarshal(data, to); err != nil {
		return err
	}
	return nil
}

// encode multiple arguments(arguments to a function).
func (th *hostEnvImpl) encodeArgs(args []interface{}) ([]byte, error) {
	return th.encode(args)
}

// decode multiple arguments(arguments to a function).
func (th *hostEnvImpl) decodeArgs(fnType reflect.Type, data []byte) (result []reflect.Value, err error) {
	var r []interface{}
argsLoop:
	for i := 0; i < fnType.NumIn(); i++ {
		argT := fnType.In(i)
		if i == 0 && (isActivityContext(argT) || isWorkflowContext(argT)) {
			continue argsLoop
		}
		arg := reflect.New(argT).Interface()
		r = append(r, arg)
	}
	err = th.decode(data, r)
	if err != nil {
		return
	}
	for i := 0; i < len(r); i++ {
		result = append(result, reflect.ValueOf(r[i]).Elem())
	}
	return
}

// encode single value(like return parameter).
func (th *hostEnvImpl) encodeArg(arg interface{}) ([]byte, error) {
	return th.encode([]interface{}{arg})
}

// decode single value(like return parameter).
func (th *hostEnvImpl) decodeArg(data []byte, to interface{}) error {
	return th.decode(data, []interface{}{to})
}

func (th *hostEnvImpl) decodeAndAssignValue(from interface{}, toValuePtr interface{}) error {
	if toValuePtr == nil {
		return nil
	}
	if rf := reflect.ValueOf(toValuePtr); rf.Type().Kind() != reflect.Ptr {
		return errors.New("value parameter provided is not a pointer")
	}
	if data, ok := from.([]byte); ok {
		if err := th.decodeArg(data, toValuePtr); err != nil {
			return err
		}
	} else if fv := reflect.ValueOf(from); fv.IsValid() {
		reflect.ValueOf(toValuePtr).Elem().Set(fv)
	}
	return nil
}

func isTypeByteSlice(inType reflect.Type) bool {
	r := reflect.TypeOf(([]byte)(nil))
	return inType == r || inType == reflect.PtrTo(r)
}

var once sync.Once

// Singleton to hold the host registration details.
var thImpl *hostEnvImpl

func newHostEnvironment() *hostEnvImpl {
	return &hostEnvImpl{
		workflowFuncMap:  make(map[string]interface{}),
		workflowAliasMap: make(map[string]string),
		activityFuncMap:  make(map[string]activity),
		activityAliasMap: make(map[string]string),
		encoding:         gobEncoding{},
	}
}

func getHostEnvironment() *hostEnvImpl {
	once.Do(func() {
		thImpl = newHostEnvironment()
	})
	return thImpl
}

// Wrapper to execute workflow functions.
type workflowExecutor struct {
	name string
	fn   interface{}
}

func (we *workflowExecutor) Execute(ctx Context, input []byte) ([]byte, error) {
	fnType := reflect.TypeOf(we.fn)
	// Workflow context.
	args := []reflect.Value{reflect.ValueOf(ctx)}

	if fnType.NumIn() > 1 && isTypeByteSlice(fnType.In(1)) {
		// 0 - is workflow context.
		// 1 ... input types.
		args = append(args, reflect.ValueOf(input))
	} else {
		decoded, err := getHostEnvironment().decodeArgs(fnType, input)
		if err != nil {
			return nil, fmt.Errorf(
				"Unable to decode the workflow function input bytes with error: %v, function name: %v",
				err, we.name)
		}
		args = append(args, decoded...)
	}

	// Invoke the workflow with arguments.
	fnValue := reflect.ValueOf(we.fn)
	retValues := fnValue.Call(args)
	return validateFunctionAndGetResults(we.fn, retValues)
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

func (ae *activityExecutor) Execute(ctx context.Context, input []byte) ([]byte, error) {
	fnType := reflect.TypeOf(ae.fn)
	args := []reflect.Value{}

	// activities optionally might not take context.
	if fnType.NumIn() > 0 && isActivityContext(fnType.In(0)) {
		args = append(args, reflect.ValueOf(ctx))
	}

	if fnType.NumIn() == 1 && isTypeByteSlice(fnType.In(0)) {
		args = append(args, reflect.ValueOf(input))
	} else {
		decoded, err := getHostEnvironment().decodeArgs(fnType, input)
		if err != nil {
			return nil, fmt.Errorf(
				"Unable to decode the activity function input bytes with error: %v for function name: %v",
				err, ae.name)
		}
		args = append(args, decoded...)
	}

	// Invoke the activity with arguments.
	fnValue := reflect.ValueOf(ae.fn)
	retValues := fnValue.Call(args)
	return validateFunctionAndGetResults(ae.fn, retValues)
}

// aggregatedWorker combines management of both workflowWorker and activityWorker worker lifecycle.
type aggregatedWorker struct {
	workflowWorker Worker
	activityWorker Worker
	logger         *zap.Logger
	hostEnv        *hostEnvImpl
}

func (aw *aggregatedWorker) Start() error {
	if !isInterfaceNil(aw.workflowWorker) {
		if len(aw.hostEnv.getRegisteredWorkflowTypes()) == 0 {
			aw.logger.Warn(
				"Starting worker without any workflows. Workflows must be registered before start.",
			)
		}
		if err := aw.workflowWorker.Start(); err != nil {
			return err
		}
	}
	if !isInterfaceNil(aw.activityWorker) {
		if len(aw.hostEnv.getRegisteredActivities()) == 0 {
			aw.logger.Warn(
				"Starting worker without any activities. Activities must be registered before start.",
			)
		}
		if err := aw.activityWorker.Start(); err != nil {
			// stop workflow worker.
			aw.workflowWorker.Stop()
			return err
		}
	}
	aw.logger.Info("Started Worker")
	return nil
}

func (aw *aggregatedWorker) Run() error {
	if err := aw.Start(); err != nil {
		return err
	}
	d := <-getKillSignal()
	aw.logger.Info("Worker has been killed", zap.String("Signal", d.String()))
	aw.Stop()
	return nil
}

func (aw *aggregatedWorker) Stop() {
	if !isInterfaceNil(aw.workflowWorker) {
		aw.workflowWorker.Stop()
	}
	if !isInterfaceNil(aw.activityWorker) {
		aw.activityWorker.Stop()
	}
	aw.logger.Info("Stopped Worker")
}

// aggregatedWorker returns an instance to manage the workers. Use defaultConcurrentPollRoutineSize (which is 2) as
// poller size. The typical RTT (round-trip time) is below 1ms within data center. And the poll API latency is about 5ms.
// With 2 poller, we could achieve around 300~400 RPS.
func newAggregatedWorker(
	service m.TChanWorkflowService,
	domain string,
	taskList string,
	options WorkerOptions,
) (worker Worker) {
	wOptions := fillWorkerOptionsDefaults(options)
	workerParams := workerExecutionParameters{
		TaskList:                                taskList,
		ConcurrentPollRoutineSize:               defaultConcurrentPollRoutineSize,
		ConcurrentActivityExecutionSize:         wOptions.MaxConcurrentActivityExecutionSize,
		MaxActivityExecutionRate:                wOptions.MaxActivityExecutionRate,
		MaxActivityExecutionRateRefreshDuration: wOptions.MaxActivityExecutionRateRefreshDuration,
		Identity:              wOptions.Identity,
		MetricsScope:          wOptions.MetricsScope,
		Logger:                wOptions.Logger,
		EnableLoggingInReplay: wOptions.EnableLoggingInReplay,
		UserContext:           wOptions.BackgroundActivityContext,
	}

	ensureRequiredParams(&workerParams)
	workerParams.MetricsScope = tagScope(workerParams.MetricsScope, tagDomain, domain)
	workerParams.Logger = workerParams.Logger.With(
		zapcore.Field{Key: tagDomain, Type: zapcore.StringType, String: domain},
		zapcore.Field{Key: tagTaskList, Type: zapcore.StringType, String: taskList},
		zapcore.Field{Key: tagWorkerID, Type: zapcore.StringType, String: workerParams.Identity},
	)
	logger := workerParams.Logger

	processTestTags(&wOptions, &workerParams)

	hostEnv := getHostEnvironment()
	// workflow factory.
	var workflowWorker Worker
	if !wOptions.DisableWorkflowWorker {
		testTags := getTestTags(wOptions.BackgroundActivityContext)
		if testTags != nil && len(testTags) > 0 {
			workflowWorker = newWorkflowWorkerWithPressurePoints(
				service,
				domain,
				workerParams,
				testTags,
				hostEnv,
			)
		} else {
			workflowWorker = newWorkflowWorker(
				service,
				domain,
				workerParams,
				nil,
				hostEnv,
			)
		}
	}

	// activity types.
	var activityWorker Worker

	if !wOptions.DisableActivityWorker {
		activityWorker = newActivityWorker(
			service,
			domain,
			workerParams,
			nil,
			hostEnv,
		)
	}
	return &aggregatedWorker{
		workflowWorker: workflowWorker,
		activityWorker: activityWorker,
		logger:         logger,
		hostEnv:        hostEnv,
	}
}

func tagScope(metricsScope tally.Scope, tagName, tagValue string) tally.Scope {
	if metricsScope == nil {
		metricsScope = tally.NoopScope
	}
	tagsMap := map[string]string{tagName: tagValue}
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
						ep.ConcurrentPollRoutineSize = size
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
	return inType.Implements(errorElem)
}

func getFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

func isInterfaceNil(i interface{}) bool {
	return i == nil || reflect.ValueOf(i).IsNil()
}

// encoding is capable of encoding and decoding objects
type encoding interface {
	Register(obj interface{}) error
	Marshal([]interface{}) ([]byte, error)
	Unmarshal([]byte, []interface{}) error
}

// gobEncoding encapsulates gob encoding and decoding
type gobEncoding struct {
}

// Register implements the encoding interface
func (g gobEncoding) Register(obj interface{}) error {
	gob.Register(obj)
	return nil
}

// Marshal encodes an array of object into bytes
func (g gobEncoding) Marshal(objs []interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	for i, obj := range objs {
		if err := enc.Encode(obj); err != nil {
			return nil, fmt.Errorf(
				"unable to encode argument: %d, %v, with gob error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a byte array into the passed in objects
// TODO: To deal with different number of arguments, may be encode number of arguments as a first value as well.
// so we can decode if a ssubset of them are asked.
func (g gobEncoding) Unmarshal(data []byte, objs []interface{}) error {
	dec := gob.NewDecoder(bytes.NewBuffer(data))
	for i, obj := range objs {
		if err := dec.Decode(obj); err != nil {
			return fmt.Errorf(
				"unable to decode argument: %d, %v, with gob error: %v", i, reflect.TypeOf(obj), err)
		}
	}
	return nil
}

func isThriftType(v interface{}) bool {
	// NOTE: Thrift serialization works only if the values are pointers.
	// Thrift has a validation that it meets thift.TStruct which has Read/Write pointer receivers.

	if reflect.ValueOf(v).Kind() != reflect.Ptr {
		return false
	}
	t := reflect.TypeOf((*thrift.TStruct)(nil)).Elem()
	return reflect.TypeOf(v).Implements(t)
}

// thriftEncoding encapsulates thrift serializer/de-serializer.
type thriftEncoding struct{}

// Register implements the encoding interface
func (g thriftEncoding) Register(obj interface{}) error {
	return nil
}

// Marshal encodes an array of thrift into bytes
func (g thriftEncoding) Marshal(objs []interface{}) ([]byte, error) {
	tlist := []thrift.TStruct{}
	for i := 0; i < len(objs); i++ {
		if !isThriftType(objs[i]) {
			return nil, fmt.Errorf("pointer to thrift.TStruct type is required for %v argument", i+1)
		}
		t := reflect.ValueOf(objs[i]).Interface().(thrift.TStruct)
		tlist = append(tlist, t)
	}
	return common.TListSerialize(tlist)
}

// Unmarshal decodes an array of thrift into bytes
func (g thriftEncoding) Unmarshal(data []byte, objs []interface{}) error {
	tlist := []thrift.TStruct{}
	for i := 0; i < len(objs); i++ {
		rVal := reflect.ValueOf(objs[i])
		if rVal.Kind() != reflect.Ptr || !isThriftType(reflect.Indirect(rVal).Interface()) {
			return fmt.Errorf("pointer to pointer thrift.TStruct type is required for %v argument", i+1)
		}
		t := reflect.New(rVal.Elem().Type().Elem()).Interface().(thrift.TStruct)
		tlist = append(tlist, t)
	}

	if err := common.TListDeserialize(tlist, data); err != nil {
		return err
	}

	for i := 0; i < len(tlist); i++ {
		reflect.ValueOf(objs[i]).Elem().Set(reflect.ValueOf(tlist[i]))
	}

	return nil
}

func fillWorkerOptionsDefaults(options WorkerOptions) WorkerOptions {
	if options.MaxConcurrentActivityExecutionSize == 0 {
		options.MaxConcurrentActivityExecutionSize = defaultMaxConcurrentActivityExecutionSize
	}
	if options.MaxActivityExecutionRate == 0 {
		options.MaxActivityExecutionRate = defaultMaxActivityExecutionRate
	}
	if options.MaxActivityExecutionRateRefreshDuration == 0 {
		options.MaxActivityExecutionRateRefreshDuration = time.Second
	}
	return options
}

type contextKey string

const testTagsContextKey = contextKey("testTags")

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
