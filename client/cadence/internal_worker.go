package cadence

// All code in this file is private to the package.

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"

	"bytes"
	"encoding/gob"
	"github.com/Sirupsen/logrus"
	"github.com/uber-common/bark"
	m "github.com/uber-go/cadence-client/.gen/go/cadence"
	"github.com/uber-go/tally"
	"strconv"
)

const (
	defaultConcurrentPollRoutineSize          = 1
	defaultMaxConcurrentActivityExecutionSize = 10000  // Large execution size(unlimited)
	defaultMaxActivityExecutionRate           = 100000 // Large execution rate(100K per sec)
)

// Assert that structs do indeed implement the interfaces
var _ WorkerOptions = (*workerOptions)(nil)
var _ Lifecycle = (*aggregatedWorker)(nil)
var _ hostEnv = (*hostEnvImpl)(nil)

type (
	// WorkflowWorker wraps the code for hosting workflow types.
	// And worker is mapped 1:1 with task list. If the user want's to poll multiple
	// task list names they might have to manage 'n' workers for 'n' task lists.
	workflowWorker struct {
		executionParameters WorkerExecutionParameters
		workflowService     m.TChanWorkflowService
		poller              taskPoller // taskPoller to poll the tasks.
		worker              *baseWorker
		identity            string
	}

	// activityRegistry collection of activity implementations
	activityRegistry map[string]Activity

	// ActivityWorker wraps the code for hosting activity types.
	// TODO: Worker doing heartbeating automatically while activity task is running
	activityWorker struct {
		executionParameters WorkerExecutionParameters
		activityRegistry    activityRegistry
		workflowService     m.TChanWorkflowService
		poller              *activityTaskPoller
		worker              *baseWorker
		identity            string
	}

	// Worker overrides.
	workerOverrides struct {
		workflowTaskHander  WorkflowTaskHandler
		activityTaskHandler ActivityTaskHandler
	}
)

// NewWorkflowTaskWorker returns an instance of a workflow task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewWorkflowTaskWorker(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) (worker Lifecycle) {
	return newWorkflowTaskWorkerInternal(taskHandler, service, params)
}

// newWorkflowWorker returns an instance of the workflow worker.
func newWorkflowWorker(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	ppMgr pressurePointMgr,
) Lifecycle {
	return newWorkflowWorkerInternal(factory, service, params, ppMgr, nil)
}

func ensureRequiredParams(params *WorkerExecutionParameters) {
	if params.Identity == "" {
		params.Identity = getWorkerIdentity(params.TaskList)
	}
	if params.Logger == nil {
		log := logrus.New()
		params.Logger = bark.NewLoggerFromLogrus(log)
		params.Logger.Info("No logger configured for cadence worker. Created default one.")
	}
}

func newWorkflowWorkerInternal(
	factory workflowDefinitionFactory,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	ppMgr pressurePointMgr,
	overrides *workerOverrides,
) Lifecycle {
	// Get a workflow task handler.
	ensureRequiredParams(&params)
	var taskHandler WorkflowTaskHandler
	if overrides != nil && overrides.workflowTaskHander != nil {
		taskHandler = overrides.workflowTaskHander
	} else {
		taskHandler = newWorkflowTaskHandler(factory, params, ppMgr)
	}
	return newWorkflowTaskWorkerInternal(taskHandler, service, params)
}

func newWorkflowTaskWorkerInternal(
	taskHandler WorkflowTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) Lifecycle {
	ensureRequiredParams(&params)
	poller := newWorkflowTaskPoller(
		taskHandler,
		service,
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

func newActivityWorkerInternal(
	activities []Activity,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
	overrides *workerOverrides,
) Lifecycle {
	ensureRequiredParams(&params)
	// Get a activity task handler.
	var taskHandler ActivityTaskHandler
	if overrides != nil && overrides.activityTaskHandler != nil {
		taskHandler = overrides.activityTaskHandler
	} else {
		taskHandler = newActivityTaskHandler(activities, service, params)
	}
	return NewActivityTaskWorker(taskHandler, service, params)
}

// NewActivityTaskWorker returns instance of an activity task handler worker.
// To be used by framework level code that requires access to the original workflow task.
func NewActivityTaskWorker(
	taskHandler ActivityTaskHandler,
	service m.TChanWorkflowService,
	params WorkerExecutionParameters,
) Lifecycle {
	ensureRequiredParams(&params)

	poller := newActivityTaskPoller(
		taskHandler,
		service,
		params,
	)
	worker := newBaseWorker(baseWorkerOptions{
		routineCount:    params.ConcurrentPollRoutineSize,
		taskPoller:      poller,
		workflowService: service,
		identity:        params.Identity,
		workerType:      "ActivityWorker"},
		params.Logger)

	return &activityWorker{
		executionParameters: params,
		activityRegistry:    make(map[string]Activity),
		workflowService:     service,
		worker:              worker,
		poller:              poller,
		identity:            params.Identity,
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
	logger                     bark.Logger
	disableWorkflowWorker      bool
	disableActivityWorker      bool
	testTags                   map[string]map[string]string
}

// NewWorkerOptionsInternal creates an instance of worker options with default values.
func NewWorkerOptionsInternal(testTags map[string]map[string]string) *workerOptions {
	return &workerOptions{
		maxConcurrentActivityExecutionSize: defaultMaxConcurrentActivityExecutionSize,
		maxActivityExecutionRate:           defaultMaxActivityExecutionRate,
		autoHeartBeatForActivities:         false,
		testTags:                           testTags,
		// Defaults for metrics, identity, logger is filled in by the WorkflowWorker APIs.
	}
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
func (wo *workerOptions) SetLogger(logger bark.Logger) WorkerOptions {
	wo.logger = logger
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
	Encoder() Encoding
	RegisterFnType(fnType reflect.Type) error
}

// hostEnvImpl is the implementation of hostEnv
type hostEnvImpl struct {
	sync.Mutex
	workerFuncMap   map[string]interface{}
	activityFuncMap map[string]interface{}
	encoding        gobEncoding
}

func (th *hostEnvImpl) RegisterWorkflow(wf interface{}) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(wf)
	if err := th.validateFnFormat(fnType, true); err != nil {
		return err
	}
	// Check if already registered
	fnName := getFunctionName(wf)
	if _, ok := th.getWorkflowFn(fnName); ok {
		return nil
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return nil
	}
	th.addWorkflowFn(fnName, wf)
	return nil
}

func (th *hostEnvImpl) RegisterActivity(af interface{}) error {
	// Validate that it is a function
	fnType := reflect.TypeOf(af)
	if err := th.validateFnFormat(fnType, false); err != nil {
		return err
	}
	// Check if already registered
	fnName := getFunctionName(af)
	if _, ok := th.getActivityFn(fnName); ok {
		return nil
	}
	// Register args with encoding.
	if err := th.registerEncodingTypes(fnType); err != nil {
		return nil
	}
	th.addActivityFn(fnName, af)
	return nil
}

// Get the encoder.
func (th *hostEnvImpl) Encoder() Encoding {
	return th.encoding
}

// Register all function args and return types with encoder.
func (th *hostEnvImpl) RegisterFnType(fnType reflect.Type) error {
	return th.registerEncodingTypes(fnType)
}

func (th *hostEnvImpl) addWorkflowFn(fnName string, wf interface{}) {
	th.Lock()
	defer th.Unlock()
	th.workerFuncMap[fnName] = wf
}

func (th *hostEnvImpl) getWorkflowFn(fnName string) (interface{}, bool) {
	th.Lock()
	defer th.Unlock()
	fn, ok := th.workerFuncMap[fnName]
	return fn, ok
}

func (th *hostEnvImpl) lenWorkflowFns() int {
	th.Lock()
	defer th.Unlock()
	return len(th.workerFuncMap)
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

// register all the types with encoder.
func (th *hostEnvImpl) registerEncodingTypes(fnType reflect.Type) error {
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
func (th *hostEnvImpl) validateFnFormat(fnType reflect.Type, isWorkflow bool) error {
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

// To hold the host registration details.
var thImpl *hostEnvImpl

func getHostEnvironment() hostEnv {
	// TODO: Make it singleton.
	if thImpl == nil {
		thImpl = &hostEnvImpl{
			workerFuncMap:   make(map[string]interface{}),
			activityFuncMap: make(map[string]interface{}),
			encoding:        gobEncoding{},
		}
		// TODO: Find a better way to register.
		fn := fnSignature{}
		thImpl.encoding.Register(fn.Args)
	}
	return thImpl
}

func setHostEnvironment(t *hostEnvImpl) {
	thImpl = t
}

// fnSignature represents a function and its arguments
type fnSignature struct {
	FnName string
	Args   []interface{}
}

// Wrapper to execute workflow functions.
type workflowExecutor struct {
	name string
	fn   interface{}
}

func (we *workflowExecutor) Execute(ctx Context, input []byte) ([]byte, error) {
	var fs fnSignature
	if err := getHostEnvironment().Encoder().Unmarshal(input, &fs); err != nil {
		return nil, fmt.Errorf(
			"Unable to decode the workflow function input bytes with error: %v, function name: %v",
			err, we.name)
	}

	targetArgs := []reflect.Value{reflect.ValueOf(ctx)}
	// rest of the parameters.
	for _, arg := range fs.Args {
		targetArgs = append(targetArgs, reflect.ValueOf(arg))
	}

	// Invoke the workflow with arguments.
	fnValue := reflect.ValueOf(we.fn)
	retValues := fnValue.Call(targetArgs)
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
	var fs fnSignature
	if err := getHostEnvironment().Encoder().Unmarshal(input, &fs); err != nil {
		return nil, fmt.Errorf(
			"Unable to decode the activity function input bytes with error: %v for function name: %v",
			err, ae.name)
	}

	targetArgs := []reflect.Value{}
	// activities optionally might not take context.
	fnType := reflect.TypeOf(ae.fn)
	if fnType.NumIn() > 0 && isActivityContext(fnType.In(0)) {
		targetArgs = append(targetArgs, reflect.ValueOf(ctx))
	}
	// rest of the parameters.
	for _, arg := range fs.Args {
		targetArgs = append(targetArgs, reflect.ValueOf(arg))
	}

	// Invoke the activity with arguments.
	fnValue := reflect.ValueOf(ae.fn)
	retValues := fnValue.Call(targetArgs)
	return validateFunctionAndGetResults(ae.fn, retValues)
}

// aggregatedWorker combines management of both workflowWorker and activityWorker worker lifecycle.
type aggregatedWorker struct {
	workflowWorker Lifecycle
	activityWorker Lifecycle
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
	groupName string,
	options WorkerOptions,
) (worker Lifecycle) {
	wOptions := options.(*workerOptions)
	workerParams := WorkerExecutionParameters{
		TaskList:                  groupName,
		ConcurrentPollRoutineSize: defaultConcurrentPollRoutineSize,
		Identity:                  wOptions.identity,
		MetricsScope:              wOptions.metricsScope,
		Logger:                    wOptions.logger,
	}

	processTestTags(wOptions, &workerParams)

	// workflow factory.
	var workflowWorker Lifecycle
	if !wOptions.disableWorkflowWorker && thImpl.lenWorkflowFns() > 0 {
		workflowFactory := func(wt WorkflowType) (Workflow, error) {
			wf, ok := thImpl.getWorkflowFn(wt.Name)
			if !ok {
				return nil, fmt.Errorf("Unable to find workflow type: %v", wt.Name)
			}
			return &workflowExecutor{name: wt.Name, fn: wf}, nil
		}
		if wOptions.testTags != nil && len(wOptions.testTags) > 0 {
			workflowWorker = NewWorkflowWorkerWithPressurePoints(
				workflowFactory,
				service,
				workerParams,
				wOptions.testTags,
			)
		} else {
			workflowWorker = NewWorkflowWorker(
				workflowFactory,
				service,
				workerParams,
			)
		}
	}

	// activity types.
	var activityWorker Lifecycle
	activityTypes := []Activity{}

	if !wOptions.disableActivityWorker {
		thImpl.Lock()
		for name, af := range thImpl.activityFuncMap {
			activityTypes = append(activityTypes, &activityExecutor{name: name, fn: af})
		}
		thImpl.Unlock()

		if len(activityTypes) > 0 {
			activityWorker = NewActivityWorker(
				activityTypes,
				service,
				workerParams,
			)
		}
	}
	return &aggregatedWorker{workflowWorker: workflowWorker, activityWorker: activityWorker}
}

func processTestTags(wOptions *workerOptions, ep *WorkerExecutionParameters) {
	if wOptions.testTags != nil {
		if paramsOverride, ok := wOptions.testTags[WorkerOptionsConfig]; ok {
			for key, val := range paramsOverride {
				switch key {
				case WorkerOptionsConfigConcurrentPollRoutineSize:
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

// Encoding is capable of encoding and decoding objects
type Encoding interface {
	Register(obj interface{}) error
	Marshal(interface{}) ([]byte, error)
	Unmarshal([]byte, interface{}) error
}

// gobEncoding encapsulates gob encoding and decoding
type gobEncoding struct {
}

// Register implements the Encoding interface
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