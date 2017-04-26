package cadence

// All code in this file is private to the package.

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"

	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	"github.com/uber-go/tally"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultConcurrentPollRoutineSize          = 1
	defaultMaxConcurrentActivityExecutionSize = 10000  // Large execution size(unlimited)
	defaultMaxActivityExecutionRate           = 100000 // Large execution rate(100K per sec)
)

// Assert that structs do indeed implement the interfaces
var _ WorkerOptions = (*workerOptions)(nil)
var _ Worker = (*aggregatedWorker)(nil)
var _ hostEnv = (*hostEnvImpl)(nil)

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
		poller              taskPoller // taskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
	}

	// activityRegistry collection of activity implementations
	activityRegistry map[string]activity

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	activityWorker struct {
		executionParameters workerExecutionParameters
		activityRegistry    activityRegistry
		workflowService     m.TChanWorkflowService
		domain              string
		poller              *activityTaskPoller
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

		// Defines how many executions for task list by this worker.
		// TODO: In future we want to separate the activity executions as they take longer than polls.
		// ConcurrentExecutionRoutineSize int

		// User can provide an identity for the debuggability. If not provided the framework has
		// a default option.
		Identity string

		MetricsScope tally.Scope

		Logger *zap.Logger

		// Enable logging in replay mode
		EnableLoggingInReplay bool
	}
)

// newWorkflowWorker returns an instance of the workflow worker.
func newWorkflowWorker(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	ppMgr pressurePointMgr,
) Worker {
	return newWorkflowWorkerInternal(factory, service, domain, params, ppMgr, nil)
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
}

func newWorkflowWorkerInternal(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	ppMgr pressurePointMgr,
	overrides *workerOverrides,
) Worker {
	// Get a workflow task handler.
	ensureRequiredParams(&params)
	var taskHandler WorkflowTaskHandler
	if overrides != nil && overrides.workflowTaskHandler != nil {
		taskHandler = overrides.workflowTaskHandler
	} else {
		taskHandler = newWorkflowTaskHandler(factory, params, ppMgr)
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
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        params.Identity,
		workerType:      "DecisionWorker"},
		params.Logger)

	return &workflowWorker{
		executionParameters: params,
		workflowService:     service,
		poller:              poller,
		worker:              worker,
		identity:            params.Identity,
	}
}

// Start the worker.
func (ww *workflowWorker) Start() error {
	ww.worker.Start()
	return nil // TODO: propagate error
}

// Shutdown the worker.
func (ww *workflowWorker) Stop() {
	ww.worker.Stop()
}

func newActivityWorker(
	activities []activity,
	service m.TChanWorkflowService,
	domain string,
	params workerExecutionParameters,
	overrides *workerOverrides,
) Worker {
	ensureRequiredParams(&params)
	// Get a activity task handler.
	var taskHandler ActivityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(activities, service, params)
	}
	return newActivityTaskWorker(taskHandler, service, domain, params)
}

func newActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	domain string,
	workerParams workerExecutionParameters) (worker Worker) {

	poller := newActivityTaskPoller(
		taskHandler,
		service,
		domain,
		workerParams,
	)
	base := newBaseWorker(baseWorkerOptions{
		routineCount:    workerParams.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        workerParams.Identity,
		workerType:      "ActivityWorker"},
		workerParams.Logger)

	return &activityWorker{
		executionParameters: workerParams,
		activityRegistry:    make(map[string]activity),
		workflowService:     service,
		worker:              base,
		poller:              poller,
		identity:            workerParams.Identity,
	}
}

// Start the worker.
func (aw *activityWorker) Start() error {
	aw.worker.Start()
	return nil // TODO: propagate errors
}

// Shutdown the worker.
func (aw *activityWorker) Stop() {
	aw.worker.Stop()
}

// workerOptions stores all host-specific parameters that cadence can use to run the workflows
// and activities and if they need any rate limiting.
type workerOptions struct {
	maxConcurrentActivityExecutionSize int
	maxActivityExecutionRate           float32
	// TODO: Move heart beating to per activity options when they are exposed.
	autoHeartBeatForActivities bool
	identity                   string
	metricsScope               tally.Scope
	logger                     *zap.Logger
	enableLoggingInReplay      bool
	disableWorkflowWorker      bool
	disableActivityWorker      bool
	testTags                   map[string]map[string]string
}

// SetMaxConcurrentActivityExecutionSize sets the maximum concurrent activity executions this host can have.
func (wo *workerOptions) SetMaxConcurrentActivityExecutionSize(size int) WorkerOptions {
	wo.maxConcurrentActivityExecutionSize = size
	return wo
}

// SetMaxActivityExecutionRate sets the rate limiting on number of activities that can be executed.
func (wo *workerOptions) SetMaxActivityExecutionRate(requestPerSecond float32) WorkerOptions {
	wo.maxActivityExecutionRate = requestPerSecond
	return wo
}

func (wo *workerOptions) SetAutoHeartBeat(auto bool) WorkerOptions {
	wo.autoHeartBeatForActivities = auto
	return wo
}

// SetIdentity identifies the host for debugging.
func (wo *workerOptions) SetIdentity(identity string) WorkerOptions {
	wo.identity = identity
	return wo
}

// SetMetrics is the metrics that the client can use to report.
func (wo *workerOptions) SetMetrics(metricsScope tally.Scope) WorkerOptions {
	wo.metricsScope = metricsScope
	return wo
}

// SetLogger sets the logger for the framework.
func (wo *workerOptions) SetLogger(logger *zap.Logger) WorkerOptions {
	wo.logger = logger
	return wo
}

// SetEnableLoggingInReplay sets the logger for the framework.
func (wo *workerOptions) SetEnableLoggingInReplay(enableLoggingInReplay bool) WorkerOptions {
	wo.enableLoggingInReplay = enableLoggingInReplay
	return wo
}

// SetDisableWorkflowWorker disables running workflow workers.
func (wo *workerOptions) SetDisableWorkflowWorker(disable bool) WorkerOptions {
	wo.disableWorkflowWorker = disable
	return wo
}

// SetDisableActivityWorker disables running activity workers.
func (wo *workerOptions) SetDisableActivityWorker(disable bool) WorkerOptions {
	wo.disableActivityWorker = disable
	return wo
}

// SetTestTags test tags for worker.
func (wo *workerOptions) SetTestTags(tags map[string]map[string]string) WorkerOptions {
	wo.testTags = tags
	return wo
}

type workerFunc func(ctx Context, input []byte) ([]byte, error)
type activityFunc func(ctx context.Context, input []byte) ([]byte, error)

// hostEnv stores all worker-specific parameters that will
// be stored inside of a context.
type hostEnv interface {
	RegisterWorkflow(wf interface{}) error
	RegisterActivity(af interface{}) error
	// TODO: (Siva) This encoder should be pluggable.
	Encoder() encoding
	RegisterFnType(fnType reflect.Type) error
}

type interceptorFn func(name string, workflow interface{}) (string, interface{})

// hostEnvImpl is the implementation of hostEnv
type hostEnvImpl struct {
	sync.Mutex
	workflowFuncMap                  map[string]interface{}
	activityFuncMap                  map[string]interface{}
	encoding                         gobEncoding
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
	th.activityFuncMap = make(map[string]interface{}) // clear map
	th.Unlock()
	for w, f := range funcMapCopy {
		intw, intf := i(w, f)
		th.Lock()
		th.activityFuncMap[intw] = intf
		th.Unlock()
	}
}

func (th *hostEnvImpl) RegisterWorkflow(af interface{}) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := validateFnFormat(fnType, true); err != nil {
		return err
	}
	// Check if already registered
	fnName := getFunctionName(af)
	if _, ok := th.getWorkflowFn(fnName); ok {
		return fmt.Errorf("workflow type \"%v\" is already registered", fnName)
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return err
	}
	fnName, af = th.invokeInterceptors(fnName, af, th.workflowRegistrationInterceptors)
	th.addWorkflowFn(fnName, af)
	return nil
}

func (th *hostEnvImpl) RegisterActivity(af interface{}) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := validateFnFormat(fnType, false); err != nil {
		return err
	}
	// Check if already registered
	fnName := getFunctionName(af)
	if _, ok := th.getActivityFn(fnName); ok {
		return fmt.Errorf("activity type \"%v\" is already registered", fnName)
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return err
	}
	fnName, af = th.invokeInterceptors(fnName, af, th.activityRegistrationInterceptors)
	th.addActivityFn(fnName, af)
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

// Register all function args and return types with encoder.
func (th *hostEnvImpl) RegisterFnType(fnType reflect.Type) error {
	return th.registerEncodingTypes(fnType)
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

func (th *hostEnvImpl) addActivityFn(fnName string, af interface{}) {
	th.Lock()
	defer th.Unlock()
	th.activityFuncMap[fnName] = af
}

func (th *hostEnvImpl) getActivityFn(fnName string) (interface{}, bool) {
	th.Lock()
	defer th.Unlock()
	fn, ok := th.activityFuncMap[fnName]
	return fn, ok
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
		argType := fnType.In(i)
		// Interfaces cannot be registered, their implementations should be
		// https://golang.org/pkg/encoding/gob/#Register
		if argType.Kind() != reflect.Interface {
			arg := reflect.Zero(argType).Interface()
			if err := th.Encoder().Register(arg); err != nil {
				return fmt.Errorf("unable to register the message for encoding: %v", err)
			}
		}
	}
	// Register return types.
	// TODO: (Siva) We need register all concrete implementations of error, Either
	// through pre-registry (or) at the time conversion.
	for i := 0; i < fnType.NumOut(); i++ {
		argType := fnType.Out(i)
		// Interfaces cannot be registered, their implementations should be
		// https://golang.org/pkg/encoding/gob/#Register
		if argType.Kind() != reflect.Interface {
			arg := reflect.Zero(argType).Interface()
			if err := th.Encoder().Register(arg); err != nil {
				return fmt.Errorf("unable to register the message for encoding: %v", err)
			}
		}
	}

	return nil
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

func (th *hostEnvImpl) getRegisteredActivities() []activity {
	result := []activity{}
	th.Lock()
	for name, af := range thImpl.activityFuncMap {
		result = append(result, &activityExecutor{name: name, fn: af})
	}
	th.Unlock()
	return result
}

// encode a single value.
func (th *hostEnvImpl) encode(r interface{}) ([]byte, error) {
	if isTypeByteSlice(reflect.TypeOf(r)) {
		return r.([]byte), nil
	}

	// Interfaces cannot be registered, their implementations should be
	// https://golang.org/pkg/encoding/gob/#Register
	if rType := reflect.Indirect(reflect.ValueOf(r)).Type(); rType.Kind() != reflect.Interface {
		t := reflect.Zero(rType).Interface()
		if err := getHostEnvironment().Encoder().Register(t); err != nil {
			return nil, err
		}
	}
	data, err := getHostEnvironment().Encoder().Marshal(r)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// decode a single value.
func (th *hostEnvImpl) decode(data []byte, to interface{}) error {
	if isTypeByteSlice(reflect.TypeOf(to)) {
		reflect.ValueOf(to).Elem().SetBytes(data)
		return nil
	}

	// Interfaces cannot be registered, their implementations should be
	// https://golang.org/pkg/encoding/gob/#Register
	if toType := reflect.Indirect(reflect.ValueOf(to)).Type(); toType.Kind() != reflect.Interface {
		t := reflect.Zero(toType).Interface()
		if err := getHostEnvironment().Encoder().Register(t); err != nil {
			return err
		}
	}
	if err := getHostEnvironment().Encoder().Unmarshal(data, to); err != nil {
		return err
	}
	return nil
}

// encode multiple values.
// Option whether to by pass byte buffer.
func (th *hostEnvImpl) encodeArgs(args []interface{}) ([]byte, error) {
	if len(args) == 1 && isTypeByteSlice(reflect.TypeOf(args[0])) {
		return args[0].([]byte), nil
	}

	for i := 0; i < len(args); i++ {
		// Interfaces cannot be registered, their implementations should be
		// https://golang.org/pkg/encoding/gob/#Register
		if rType := reflect.Indirect(reflect.ValueOf(args[0])).Type(); rType.Kind() != reflect.Interface {
			t := reflect.Zero(rType).Interface()
			if err := getHostEnvironment().Encoder().Register(t); err != nil {
				return nil, err
			}
		}
	}

	s := fnSignature{Args: args}
	input, err := getHostEnvironment().encode(s)
	if err != nil {
		return nil, err
	}
	return input, nil
}

// decode multiple values.
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
	err = th.decodeArgsTo(data, r)
	if err != nil {
		return
	}
	for i := 0; i < len(r); i++ {
		result = append(result, reflect.ValueOf(r[i]).Elem())
	}
	return
}

// decode multiple values in to a given structure.
func (th *hostEnvImpl) decodeArgsTo(data []byte, to []interface{}) error {
	if len(to) == 1 && isTypeByteSlice(reflect.TypeOf(to[0])) {
		reflect.ValueOf(to[0]).Elem().SetBytes(data)
		return nil
	}

	for i := 0; i < len(to); i++ {
		// Interfaces cannot be registered, their implementations should be
		// https://golang.org/pkg/encoding/gob/#Register
		if rType := reflect.Indirect(reflect.ValueOf(to[0])).Type(); rType.Kind() != reflect.Interface {
			t := reflect.Zero(rType).Interface()
			if err := getHostEnvironment().Encoder().Register(t); err != nil {
				return err
			}
		}
	}

	s := fnSignature{}
	err := getHostEnvironment().decode(data, &s)
	if err != nil {
		return err
	}
	for i := 0; i < len(to); i++ {
		vto := reflect.ValueOf(to[i])
		if vto.IsValid() {
			vto.Elem().Set(reflect.ValueOf(s.Args[i]))
		}
	}
	return nil
}

// encode single value(like return parameter).
func (th *hostEnvImpl) encodeArg(arg interface{}) ([]byte, error) {
	if isTypeByteSlice(reflect.TypeOf(arg)) {
		return arg.([]byte), nil
	}

	s := fnReturnSignature{Ret: arg}
	input, err := getHostEnvironment().encode(s)
	if err != nil {
		return nil, err
	}
	return input, nil
}

// decode single value(like return parameter).
func (th *hostEnvImpl) decodeArg(data []byte, to interface{}) error {
	if isTypeByteSlice(reflect.TypeOf(to)) {
		reflect.ValueOf(to).Elem().SetBytes(data)
		return nil
	}

	s := fnReturnSignature{}
	err := getHostEnvironment().decode(data, &s)
	if err != nil {
		return err
	}
	fv := reflect.ValueOf(to)
	if fv.IsValid() {
		fv.Elem().Set(reflect.ValueOf(s.Ret))
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

func getHostEnvironment() *hostEnvImpl {
	once.Do(func() {
		thImpl = &hostEnvImpl{
			workflowFuncMap: make(map[string]interface{}),
			activityFuncMap: make(map[string]interface{}),
			encoding:        gobEncoding{},
		}
		// TODO: Find a better way to register.
		fn := fnSignature{}
		thImpl.encoding.Register(fn.Args)
	})
	return thImpl
}

// fnSignature represents a function and its arguments
type fnSignature struct {
	Args []interface{}
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
}

func (aw *aggregatedWorker) Start() error {
	if !isInterfaceNil(aw.workflowWorker) {
		if err := aw.workflowWorker.Start(); err != nil {
			return err
		}
	}
	if !isInterfaceNil(aw.activityWorker) {
		if err := aw.activityWorker.Start(); err != nil {
			// stop workflow worker.
			aw.workflowWorker.Stop()
			return err
		}
	}
	return nil
}

func (aw *aggregatedWorker) Stop() {
	if !isInterfaceNil(aw.workflowWorker) {
		aw.workflowWorker.Stop()
	}
	if !isInterfaceNil(aw.activityWorker) {
		aw.activityWorker.Stop()
	}
}

// aggregatedWorker returns an instance to manage the workers.
func newAggregatedWorker(
	service m.TChanWorkflowService,
	domain string,
	groupName string,
	options WorkerOptions,
) (worker Worker) {
	wOptions := options.(*workerOptions)
	workerParams := workerExecutionParameters{
		TaskList:                  groupName,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.identity,
		MetricsScope:              wOptions.metricsScope,
		Logger:                    wOptions.logger,
		EnableLoggingInReplay:     wOptions.enableLoggingInReplay,
	}

	processTestTags(wOptions, &workerParams)

	env := getHostEnvironment()
	// workflow factory.
	var workflowWorker Worker
	if !wOptions.disableWorkflowWorker && env.lenWorkflowFns() > 0 {
		workflowFactory := newRegisteredWorkflowFactory()
		if wOptions.testTags != nil && len(wOptions.testTags) > 0 {
			workflowWorker = newWorkflowWorkerWithPressurePoints(
				workflowFactory,
				service,
				domain,
				workerParams,
				wOptions.testTags,
			)
		} else {
			workflowWorker = newWorkflowWorker(
				getWorkflowDefinitionFactory(workflowFactory),
				service,
				domain,
				workerParams,
				nil)
		}
	}

	// activity types.
	var activityWorker Worker

	if !wOptions.disableActivityWorker {
		activityTypes := env.getRegisteredActivities()
		if len(activityTypes) > 0 {
			activityWorker = newActivityWorker(
				activityTypes,
				service,
				domain,
				workerParams,
				nil,
			)
		}
	}
	return &aggregatedWorker{workflowWorker: workflowWorker, activityWorker: activityWorker}
}

func newRegisteredWorkflowFactory() workflowFactory {
	return func(wt WorkflowType) (workflow, error) {
		env := getHostEnvironment()
		wf, ok := env.getWorkflowFn(wt.Name)
		if !ok {
			supported := strings.Join(env.getRegisteredWorkflowTypes(), ", ")
			return nil, fmt.Errorf("Unable to find workflow type: %v. Supported types: [%v]", wt.Name, supported)
		}
		return &workflowExecutor{name: wt.Name, fn: wf}, nil
	}
}

func processTestTags(wOptions *workerOptions, ep *workerExecutionParameters) {
	if wOptions.testTags != nil {
		if paramsOverride, ok := wOptions.testTags[workerOptionsConfig]; ok {
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
	Marshal(interface{}) ([]byte, error)
	Unmarshal([]byte, interface{}) error
}

// gobEncoding encapsulates gob encoding and decoding
type gobEncoding struct {
}

// Register implements the encoding interface
func (g gobEncoding) Register(obj interface{}) error {
	gob.Register(obj)
	return nil
}

// Marshal encodes an object into bytes
func (g gobEncoding) Marshal(obj interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(obj); err != nil {
		return nil, fmt.Errorf("unable to encode with gob: %v", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a byte array into the passed in object
func (g gobEncoding) Unmarshal(data []byte, obj interface{}) error {
	dec := gob.NewDecoder(bytes.NewBuffer(data))
	if err := dec.Decode(obj); err != nil {
		return fmt.Errorf("unable to decode with gob: %v", err)
	}
	return nil
}

func getWorkflowDefinitionFactory(factory workflowFactory) workflowDefinitionFactory {
	return func(workflowType WorkflowType) (workflowDefinition, error) {
		wd, err := factory(workflowType)
		if err != nil {
			return nil, err
		}
		return newWorkflowDefinition(wd), nil
	}
}
